// Ministry session bridge.
//
// finddoctors.gov.gr v2.0.3 gates every API call behind a TaxisNet session and
// rejects external HTTP clients (curl/Go) even with the cookie. The ONLY reliable
// caller is the authenticated browser. This bridge attaches to a logged-in Chrome
// (CDP) and replays the Go aggregator's upstream calls FROM the page origin
// (credentials:'include'), so the real session + fingerprint are always used.
//
// Wire-up:  MINISTRY_BASE_URL=http://localhost:8799  ./aggregator_server
//
// Connect modes:
//   - default: connectOverCDP(BRIDGE_CDP_URL || http://localhost:9222) — reuse an
//     already-open, logged-in Chrome (start Chrome with --remote-debugging-port=9222).
//   - the page is whichever tab is on finddoctors.gov.gr; otherwise a new tab is opened.
//
// Auto-refresh: on a 403 the bridge re-runs the TaxisNet login route. While the
// gsis.gr SSO is alive this is a single consent click (no password). If the SSO
// itself expired and TAXISNET_USERNAME/TAXISNET_PASSWORD are set (in .env, never
// logged), it fills the gsis login form headlessly.

import http from 'node:http';
import os from 'node:os';
import path from 'node:path';
import { chromium } from 'playwright-core';

// Connect mode: "launch" (default) opens a dedicated, persistent Chrome profile
// you log into once; "cdp" attaches to an already-running Chrome that exposes a
// standard DevTools endpoint (start it with --remote-debugging-port=9222).
const MODE = process.env.BRIDGE_CDP_URL ? 'cdp' : process.env.BRIDGE_MODE || 'launch';
const CDP_URL = process.env.BRIDGE_CDP_URL || 'http://localhost:9222';
// Silent operation. True headless does NOT work here — finddoctors' Angular SPA
// refuses to render under headless Chrome (blank DOM / bot-detection), so login
// never completes. Instead we run a REAL headed Chrome (passes anti-bot) but place
// its window FAR off-screen, so nothing pops up and no field-filling is visible.
//   BRIDGE_HEADLESS=true  → force true headless (will likely fail; for testing only)
//   BRIDGE_VISIBLE=true   → show the window on-screen (for debugging the flow)
const HEADLESS = process.env.BRIDGE_HEADLESS === 'true';
const VISIBLE = process.env.BRIDGE_VISIBLE === 'true';
const PROFILE_DIR = process.env.BRIDGE_PROFILE || path.join(os.homedir(), '.finddoctors-bridge-profile');
const PORT = Number(process.env.BRIDGE_PORT || 8799);
// Primary data path: push the live cookie set to the Go aggregator so it can call
// the Ministry DIRECTLY (the 3 finddoctors cookies are enough — no per-request proxy
// needed). Set GO_SESSION_URL='' to disable the pump and use proxy mode instead.
const GO_SESSION_URL = process.env.GO_SESSION_URL ?? 'http://localhost:8080/api/session';
const PUMP_INTERVAL_MS = Number(process.env.PUMP_INTERVAL_MS || 60000);
const ORIGIN = 'https://www.finddoctors.gov.gr';
const APP_URL = `${ORIGIN}/p-appointment/`;
const LOGIN_URL = `${ORIGIN}/p-appointment/#/patients/patients-login`;
// Patient identity for the post-TaxisNet step (the page where you confirm your
// ΑΜΚΑ and click through). Read from the environment, never logged. Set
// MINISTRY_AMKA in find_doctors_server/.env (gitignored).
const PATIENT_AMKA = (process.env.MINISTRY_AMKA || process.env.PATIENT_AMKA || '').trim();

let browser;
let page;
let refreshing = null;
let lastRefreshAt = 0;
const REFRESH_COOLDOWN_MS = 30000;

async function findOrOpenPage() {
	const contexts = browser.contexts();
	for (const ctx of contexts) {
		for (const p of ctx.pages()) {
			if (p.url().includes('finddoctors.gov.gr')) return p;
		}
	}
	const ctx = contexts[0] || (await browser.newContext());
	const p = await ctx.newPage();
	await p.goto(APP_URL, { waitUntil: 'domcontentloaded' }).catch(() => {});
	return p;
}

async function connect() {
	if (MODE === 'cdp') {
		browser = await chromium.connectOverCDP(CDP_URL);
		page = await findOrOpenPage();
		console.log(`[bridge] attached over CDP to ${CDP_URL}, page: ${page.url()}`);
		return;
	}
	// launch mode: dedicated persistent profile using the installed Google Chrome.
	// Off-screen unless explicitly headless or visible: a real rendered window that
	// the user never sees (no pop-up, no visible typing).
	const offscreen = !HEADLESS && !VISIBLE;
	const ctx = await chromium.launchPersistentContext(PROFILE_DIR, {
		channel: 'chrome',
		headless: HEADLESS,
		viewport: HEADLESS ? { width: 1280, height: 900 } : null,
		args: [
			'--no-first-run',
			'--no-default-browser-check',
			'--disable-blink-features=AutomationControlled',
			...(offscreen ? ['--window-position=-32000,-32000', '--window-size=1280,900', '--start-minimized'] : [])
		]
	});
	browser = ctx.browser() ?? { contexts: () => [ctx], newContext: async () => ctx };
	const pages = ctx.pages();
	page = pages.find((p) => p.url().includes('finddoctors.gov.gr')) ?? pages[0] ?? (await ctx.newPage());
	if (!page.url().includes('finddoctors.gov.gr')) {
		await page.goto(APP_URL, { waitUntil: 'domcontentloaded' }).catch(() => {});
	}
	console.log(`[bridge] launched persistent Chrome (${PROFILE_DIR}), page: ${page.url()}`);
	if (!(await checkSession())) {
		console.log('[bridge] NOT logged in yet → attempting TaxisNet login flow…');
		await refreshSession();
	} else {
		// TaxisNet session is warm, but patient ΑΜΚΑ identification may still be
		// pending (checkSession passes before it) — complete it if so.
		await completePatientLogin();
	}
}

// Run a fetch inside the authenticated page and return {status, body}.
async function proxyFetch(pathWithQuery, method, body) {
	const url = ORIGIN + pathWithQuery;
	return page.evaluate(
		async ({ url, method, body }) => {
			const headers = { accept: 'application/json', authorization: 'no-auth' };
			if (body) headers['content-type'] = 'application/json';
			const res = await fetch(url, { method, headers, body: body || undefined, credentials: 'include' });
			const text = await res.text();
			return { status: res.status, body: text };
		},
		{ url, method, body }
	);
}

// Best-effort session refresh. Returns true if it ended on an authenticated page.
async function refreshSession() {
	if (refreshing) return refreshing;
	// Cooldown: while unauthenticated every call 403s; don't run a slow refresh on
	// each one. One attempt per window — enough to ride out an SSO-alive expiry.
	if (Date.now() - lastRefreshAt < REFRESH_COOLDOWN_MS) return false;
	lastRefreshAt = Date.now();
	refreshing = (async () => {
		try {
			console.log('[bridge] refreshing session…');
			await page.goto(LOGIN_URL, { waitUntil: 'domcontentloaded' });
			await page.waitForTimeout(800);

			// 1) finddoctors login page → click "Είσοδος με TaxisΝet". NOTE the label uses
			// a Greek capital Νι (Ν), not Latin N, so match by the stable CSS class and
			// fall back to a Unicode-tolerant text regex.
			const taxisBtn = page
				.locator('button.taxisnet-auth-form-btn, button:has-text("Taxis")')
				.first();
			await taxisBtn.waitFor({ state: 'visible', timeout: 8000 }).catch(() => {});
			if (await taxisBtn.count().catch(() => 0)) {
				await taxisBtn.click().catch(() => {});
				await page.waitForURL(/gsis\.gr/, { timeout: 10000 }).catch(() => {});
			}

			// 2) gsis.gr login form: fill username/password (only if SSO expired AND creds
			// provided). Real field ids are j_username / j_password, submit #btn-login-submit.
			if (page.url().includes('gsis.gr')) {
				const user = process.env.TAXISNET_USERNAME;
				const pass = process.env.TAXISNET_PASSWORD;
				const userField = page.locator('#j_username, input[name="j_username"]');
				if (await userField.count().catch(() => 0)) {
					if (user && pass) {
						await userField.first().fill(user).catch(() => {});
						await page.locator('#j_password, input[name="j_password"]').first().fill(pass).catch(() => {});
					}
					const loginBtn = page.locator('#btn-login-submit');
					if (await loginBtn.count().catch(() => 0)) {
						await loginBtn.first().click().catch(() => {});
					} else {
						await page.getByRole('button', { name: /Σύνδεση|Login/i }).first().click().catch(() => {});
					}
					await page.waitForTimeout(2500);
				}
				// 3) consent / authorization page → submit (the SSO-alive happy path needs
				// no password, just this click).
				const submit = page.getByRole('button', {
					name: /Αποστολή|Συνέχεια|Continue|Έγκριση|Εξουσιοδότηση|Αποδοχή/i
				});
				if (await submit.count().catch(() => 0)) {
					await submit.first().click().catch(() => {});
				}
			}

			await page.waitForURL(/finddoctors\.gov\.gr/, { timeout: 8000 }).catch(() => {});
			await page.waitForTimeout(1000);
			// 4) post-TaxisNet patient identification: confirm ΑΜΚΑ and click through.
			await completePatientLogin();
			const ok = await checkSession();
			console.log(`[bridge] refresh ${ok ? 'OK' : 'still unauthenticated'} (${page.url()})`);
			return ok;
		} catch (e) {
			console.error('[bridge] refresh error:', e.message);
			return false;
		} finally {
			refreshing = null;
		}
	})();
	return refreshing;
}

async function checkSession() {
	try {
		const r = await proxyFetch('/p-appointment/api/v1/gen/getHealthUnitTypes/', 'GET', null);
		return r.status === 200;
	} catch {
		return false;
	}
}

// After TaxisNet, finddoctors runs a 2-step patient identification (verified live):
//   1. #/patients/amka-input — Angular Material form, input[formcontrolname="amka"]
//      (maxlength 11) + a "Συνέχεια" button that stays disabled until the ΑΜΚΑ
//      validates. We TYPE the ΑΜΚΑ (pressSequentially, so Angular's reactive
//      validators fire — fill() alone can leave it pristine/disabled).
//   2. #/patients/patient-data-confirm — name/birthdate prefilled; click "Επιβεβαίωση".
// Then it lands on the patient home (off the /patients/ identification routes).
// Conservative: needs MINISTRY_AMKA; on an unknown page it no-ops so a human can
// finish manually. NOTE: checkSession() (getHealthUnitTypes) returns 200 even
// BEFORE this step, so it must NOT gate the flow.
const IDENTIFY_ROUTE_RE = /amka-input|patient-data-confirm|patients-login/;

async function settle() {
	await page.waitForLoadState('networkidle', { timeout: 8000 }).catch(() => {});
	await page.waitForTimeout(900);
}

// Click a button once it is genuinely enabled (Material marks disabled via both the
// disabled attr and a .mat-button-disabled class). Polls so we ride out async
// validation; returns false if it never enables within the timeout.
async function clickWhenEnabled(selector, timeout = 6000) {
	const btn = page.locator(selector).first();
	const deadline = Date.now() + timeout;
	while (Date.now() < deadline) {
		if (await btn.count().catch(() => 0)) {
			const disabled = await btn.isDisabled().catch(() => true);
			const matDisabled = await btn.evaluate((b) => b.classList.contains('mat-button-disabled')).catch(() => false);
			if (!disabled && !matDisabled) {
				await btn.click().catch(() => {});
				return true;
			}
		}
		await page.waitForTimeout(300);
	}
	return false;
}

async function completePatientLogin() {
	for (let step = 0; step < 8; step++) {
		const url = page.url();

		// Step 1: ΑΜΚΑ entry.
		if (url.includes('amka-input')) {
			if (!PATIENT_AMKA) {
				console.log('[bridge] ΑΜΚΑ page reached but MINISTRY_AMKA is not set → enter it manually');
				return false;
			}
			const f = page.locator('input[formcontrolname="amka"], input[maxlength="11"]').first();
			if (await f.count().catch(() => 0)) {
				const cur = await f.inputValue().catch(() => '');
				if (cur !== PATIENT_AMKA) {
					await f.click().catch(() => {});
					await f.fill('').catch(() => {});
					await f.pressSequentially(PATIENT_AMKA, { delay: 30 }).catch(() => {});
					await f.blur().catch(() => {});
				}
				const boxes = page.locator('input[type="checkbox"]:not(:checked)');
				const bc = await boxes.count().catch(() => 0);
				for (let i = 0; i < bc; i++) await boxes.nth(i).check({ force: true }).catch(() => {});
				await clickWhenEnabled('button.auth-form-btn, button:has-text("Συνέχεια")', 6000);
			}
			await settle();
			continue;
		}

		// Step 2: confirm the prefilled patient data (do NOT touch "Επιστροφή").
		if (url.includes('patient-data-confirm')) {
			await clickWhenEnabled('button:has-text("Επιβεβαίωση")', 6000);
			await settle();
			continue;
		}

		// Any other patient gate with a generic proceed button (defensive).
		if (IDENTIFY_ROUTE_RE.test(url)) {
			const clicked = await clickWhenEnabled('button:has-text("Συνέχεια"), button:has-text("Επιβεβαίωση"), button[type="submit"]', 4000);
			if (!clicked) return false;
			await settle();
			continue;
		}

		// Off the identification routes → patient identified.
		console.log(`[bridge] patient identification complete, page: ${page.url()}`);
		return true;
	}
	return !IDENTIFY_ROUTE_RE.test(page.url());
}

// Debug aid: a structured snapshot of the current page's form controls so we can
// pin down exact selectors for the ΑΜΚΑ step without guessing. Returns inputs and
// buttons with their identifying attributes. (Values are intentionally omitted.)
async function dumpPageControls() {
	return page.evaluate(() => {
		const vis = (el) => {
			const r = el.getBoundingClientRect();
			return r.width > 0 && r.height > 0;
		};
		const inputs = [...document.querySelectorAll('input, select, textarea')].filter(vis).map((el) => ({
			tag: el.tagName.toLowerCase(),
			type: el.getAttribute('type'),
			name: el.getAttribute('name'),
			id: el.id || null,
			formcontrolname: el.getAttribute('formcontrolname'),
			placeholder: el.getAttribute('placeholder'),
			maxlength: el.getAttribute('maxlength'),
			ariaLabel: el.getAttribute('aria-label')
		}));
		const buttons = [...document.querySelectorAll('button, [role="button"], input[type="submit"]')].filter(vis).map((el) => ({
			tag: el.tagName.toLowerCase(),
			text: (el.textContent || '').trim().slice(0, 60),
			value: el.getAttribute('value'),
			class: el.getAttribute('class'),
			disabled: el.disabled ?? null
		}));
		const labels = [...document.querySelectorAll('label, h1, h2, h3')].filter(vis).map((el) => (el.textContent || '').trim()).filter(Boolean).slice(0, 30);
		return { url: location.href, title: document.title, inputs, buttons, labels };
	});
}

// Read the finddoctors cookies and hand the full Cookie header to the Go aggregator,
// which then calls the Ministry directly. Also keeps the browser session warm.
async function pumpCookies() {
	if (!GO_SESSION_URL) return;
	try {
		const cookies = await page.context().cookies();
		const fd = cookies.filter((c) => c.domain.includes('finddoctors'));
		if (!fd.length) return;
		const header = fd.map((c) => `${c.name}=${c.value}`).join('; ');
		await fetch(GO_SESSION_URL, {
			method: 'POST',
			headers: { 'content-type': 'application/json' },
			body: JSON.stringify({ cookie: header })
		}).catch(() => {});
	} catch (e) {
		console.error('[bridge] cookie pump error:', e.message);
	}
}

function readBody(req) {
	return new Promise((resolve) => {
		let data = '';
		req.on('data', (c) => (data += c));
		req.on('end', () => resolve(data));
		req.on('error', () => resolve(''));
	});
}

const server = http.createServer(async (req, res) => {
	const url = req.url || '/';
	if (url === '/bridge/health') {
		const ok = await checkSession();
		res.writeHead(200, { 'content-type': 'application/json' });
		res.end(JSON.stringify({ ok: true, hasSession: ok, page: page?.url() ?? null }));
		return;
	}
	// Debug aid: structured snapshot of the current page's form controls, to pin
	// down exact selectors for the ΑΜΚΑ step. Navigate the browser to that page,
	// then `curl localhost:8799/bridge/dump`.
	if (url === '/bridge/dump') {
		try {
			const info = await dumpPageControls();
			res.writeHead(200, { 'content-type': 'application/json' });
			res.end(JSON.stringify(info, null, 2));
		} catch (e) {
			res.writeHead(500, { 'content-type': 'application/json' });
			res.end(JSON.stringify({ error: String(e) }));
		}
		return;
	}
	// Manually (re)run the full login + patient-identification flow.
	if (url === '/bridge/login') {
		const ok = await refreshSession();
		res.writeHead(200, { 'content-type': 'application/json' });
		res.end(JSON.stringify({ ok, hasSession: ok, page: page?.url() ?? null }));
		return;
	}
	// Reverse-engineering aid: dump the FULL cookie jar (httpOnly included) so we can
	// test whether an external client can reuse the session without the browser.
	if (url === '/bridge/cookies') {
		try {
			const cookies = await page.context().cookies();
			res.writeHead(200, { 'content-type': 'application/json' });
			res.end(JSON.stringify({ url: page.url(), count: cookies.length, cookies }, null, 2));
		} catch (e) {
			res.writeHead(500, { 'content-type': 'application/json' });
			res.end(JSON.stringify({ error: String(e) }));
		}
		return;
	}
	if (!url.startsWith('/p-appointment/')) {
		res.writeHead(404, { 'content-type': 'application/json' });
		res.end('{"error":{"code":"not_found","message":"bridge only proxies /p-appointment/*"}}');
		return;
	}
	const body = req.method === 'POST' ? await readBody(req) : null;
	try {
		let r = await proxyFetch(url, req.method || 'GET', body);
		if (r.status === 403) {
			const refreshed = await refreshSession();
			if (refreshed) r = await proxyFetch(url, req.method || 'GET', body);
		}
		res.writeHead(r.status, { 'content-type': 'application/json' });
		res.end(r.body);
	} catch (e) {
		console.error('[bridge] proxy error:', e.message);
		// Page may have been closed/navigated; try to re-acquire it once.
		try {
			page = await findOrOpenPage();
		} catch {}
		res.writeHead(502, { 'content-type': 'application/json' });
		res.end(`{"error":{"code":"upstream_failure","message":"bridge proxy failed"}}`);
	}
});

await connect();
server.listen(PORT, () => console.log(`[bridge] listening on http://localhost:${PORT} → ${ORIGIN}`));

// Cookie pump: push the live cookie set to Go now and on an interval (keeps the
// aggregator's session fresh and the browser session warm).
if (GO_SESSION_URL) {
	await pumpCookies();
	setInterval(pumpCookies, PUMP_INTERVAL_MS);
	console.log(`[bridge] cookie pump → ${GO_SESSION_URL} every ${PUMP_INTERVAL_MS}ms`);
}

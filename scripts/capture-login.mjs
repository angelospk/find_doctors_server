// One-off diagnostic: capture the FULL TaxisNet → finddoctors login network flow
// (every request, redirect, POST body, and Set-Cookie) so we can judge whether the
// OAuth can be replayed with a pure HTTP client (no browser). Uses a FRESH temp
// profile so we see the complete gsis username/password flow, not an SSO shortcut.
//
// Run: node --env-file-if-exists=.env scripts/capture-login.mjs
// Output: /tmp/login-flow.json

import fs from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { chromium } from 'playwright-core';

const ORIGIN = 'https://www.finddoctors.gov.gr';
const LOGIN_URL = `${ORIGIN}/p-appointment/#/patients/patients-login`;
const PROFILE = path.join(os.tmpdir(), `fd-capture-${Date.now()}`);
const log = [];

const ctx = await chromium.launchPersistentContext(PROFILE, {
	channel: 'chrome',
	headless: false,
	viewport: { width: 1280, height: 900 },
	args: ['--no-first-run', '--no-default-browser-check', '--disable-blink-features=AutomationControlled', '--window-position=-32000,-32000', '--start-minimized']
});
const page = ctx.pages()[0] ?? (await ctx.newPage());

page.on('request', (r) => {
	const pd = r.postData();
	const url = r.url();
	log.push({
		t: 'req',
		method: r.method(),
		url: url.slice(0, 300),
		resourceType: r.resourceType(),
		postData: pd ? pd.slice(0, 500) : null,
		// Capture headers for the patient-identification API calls to compare with curl.
		headers: /\/api\/v1\/(patienterv|rv|auth)/.test(url) ? r.headers() : undefined
	});
});
page.on('response', (res) => {
	const h = res.headers();
	log.push({
		t: 'res',
		status: res.status(),
		url: res.url().slice(0, 300),
		location: h['location'] || null,
		setCookie: h['set-cookie'] ? h['set-cookie'].slice(0, 300) : null,
		contentType: h['content-type'] || null
	});
});

async function step(label, fn) {
	log.push({ t: 'step', label, url: page.url() });
	try {
		await fn();
	} catch (e) {
		log.push({ t: 'error', label, msg: e.message });
	}
}

await step('goto login', async () => {
	await page.goto(LOGIN_URL, { waitUntil: 'domcontentloaded' });
	await page.waitForTimeout(1500);
});

await step('click TaxisNet', async () => {
	const btn = page.locator('button.taxisnet-auth-form-btn, button:has-text("Taxis")').first();
	await btn.waitFor({ state: 'visible', timeout: 10000 });
	await btn.click();
	await page.waitForURL(/gsis\.gr/, { timeout: 15000 }).catch(() => {});
	await page.waitForTimeout(1500);
});

await step('gsis credentials', async () => {
	if (!page.url().includes('gsis.gr')) return;
	const user = process.env.TAXISNET_USERNAME;
	const pass = process.env.TAXISNET_PASSWORD;
	const uf = page.locator('#j_username, input[name="j_username"]');
	if (await uf.count()) {
		if (user && pass) {
			await uf.first().fill(user);
			await page.locator('#j_password, input[name="j_password"]').first().fill(pass);
		}
		const b = page.locator('#btn-login-submit');
		if (await b.count()) await b.first().click();
		else await page.getByRole('button', { name: /Σύνδεση|Login/i }).first().click().catch(() => {});
		await page.waitForTimeout(3000);
	}
});

await step('gsis consent', async () => {
	const submit = page.getByRole('button', { name: /Αποστολή|Συνέχεια|Continue|Έγκριση|Εξουσιοδότηση|Αποδοχή/i });
	if (await submit.count()) {
		await submit.first().click().catch(() => {});
		await page.waitForTimeout(3000);
	}
	await page.waitForURL(/finddoctors\.gov\.gr/, { timeout: 15000 }).catch(() => {});
	await page.waitForTimeout(2000);
});

await step('amka-input', async () => {
	if (!page.url().includes('amka-input')) return;
	const amka = (process.env.MINISTRY_AMKA || '').trim();
	const f = page.locator('input[formcontrolname="amka"], input[maxlength="11"]').first();
	if ((await f.count()) && amka) {
		await f.click();
		await f.fill('');
		await f.pressSequentially(amka, { delay: 30 });
		await f.blur();
	}
	const btn = page.locator('button.auth-form-btn, button:has-text("Συνέχεια")').first();
	for (let i = 0; i < 20; i++) {
		if ((await btn.count()) && !(await btn.isDisabled().catch(() => true))) {
			await btn.click().catch(() => {});
			break;
		}
		await page.waitForTimeout(300);
	}
	await page.waitForTimeout(2500);
});

await step('patient-data-confirm', async () => {
	if (!page.url().includes('patient-data-confirm')) return;
	await page.locator('button:has-text("Επιβεβαίωση")').first().click().catch(() => {});
	await page.waitForTimeout(2500);
});

log.push({ t: 'final', url: page.url() });
// Summarize the cross-origin hops + cookie-setting responses for quick reading.
const hops = log.filter((e) => e.t === 'res' && (e.status >= 300 && e.status < 400 || e.setCookie)).map((e) => ({ status: e.status, url: e.url, location: e.location, setCookie: e.setCookie ? e.setCookie.split('=')[0] : null }));
fs.writeFileSync('/tmp/login-flow.json', JSON.stringify({ final: page.url(), hops, full: log }, null, 2));
console.log(`[capture] wrote /tmp/login-flow.json — ${log.length} events, final ${page.url()}`);
await ctx.close();
fs.rmSync(PROFILE, { recursive: true, force: true });

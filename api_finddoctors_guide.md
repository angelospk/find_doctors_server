# Πλήρης & Αναλυτικός Οδηγός API για το finddoctors.gov.gr

Αυτός ο οδηγός δημιουργήθηκε με σκοπό την πλήρη τεκμηρίωση (reverse engineering) του API που χρησιμοποιεί το Ελληνικό Υπουργείο Υγείας για τα ηλεκτρονικά ραντεβού στα νοσοκομεία και τις μονάδες ΠΦΥ (Πρωτοβάθμιας Φροντίδας Υγείας).

**Κύριο Εύρημα (Breakthrough):**
Το σύστημα διαθέτει έναν "Hybrid" μηχανισμό ασφαλείας. Σε αντίθεση με ό,τι πιστεύαμε αρχικά, **ΚΑΝΕΝΑ από τα endpoints αναζήτησης και εύρεσης διαθεσιμότητας δεν απαιτεί TaxisNet Cookie**. Μπορείς να τραβήξεις όλα τα δεδομένα ελεύθερα, απλώς προσομοιώνοντας τα headers του browser (ειδικά τα `origin`, `referer` και `user-agent`). Cookie απαιτείται **μόνο** στη φάση της τελικής κράτησης (`/rv/bookappointment`), όπου εμπλέκονται τα προσωπικά δεδομένα (ΑΜΚΑ).

Αυτό σημαίνει ότι μπορούμε να χτίσουμε έναν πανίσχυρο, ελεύθερο aggregator που θα συγκεντρώνει τηλεμετρία για όλη την Ελλάδα.

---

## 1. Βασικές Έννοιες & Παράμετροι

Το API είναι case-sensitive και δυστυχώς έχει ασυνέπειες στην ονοματολογία των παραμέτρων (CamelCase vs lowercase). Πρέπει να προσέχεις ακριβώς πώς γράφεται το κάθε κλειδί σε κάθε Endpoint (π.χ. αλλού είναι `specialityID` και αλλού `specialityId`).

- **`prefectureID` / `prefectureId`**: Ο κωδικός του Νομού. (π.χ. 5 = Αττική, 11 = Έβρος). Το `null` λειτουργεί σε κάποια endpoints για "Πανελλαδική Αναζήτηση".
- **`specialityID` / `specialtyId` / `spec`**: Ο εσωτερικός κωδικός της ειδικότητας. (π.χ. 10 = Νευρολόγος, 12 = Ουρολόγος).
- **`hunit` / `hunitId`**: Το μοναδικό ID της μονάδας υγείας (Νοσοκομείο ή Κέντρο Υγείας). Π.χ. 21 = Ευαγγελισμός, 718 = Π.Γ.Ν. Αλεξανδρούπολης.
- **`foreasID` / `foreas` / `hUnitType`**: Ο τύπος του φορέα. **Πλήρης λίστα** (από `/gen/getHealthUnitTypes/`, επιβεβαιωμένο live):
  | hUnitType | name                                  | isActive (May 2026) |
  |-----------|---------------------------------------|---------------------|
  | `1`       | Νοσοκομείο (ΕΣΥ)                       | 1                   |
  | `18`      | Δημόσιες Δομές (ΠΦΥ / Κέντρα Υγείας)   | 0 *                 |
  | `19`      | Ιδιώτες συμβεβλημένοι με ΕΟΠΥΥ         | 0 *                 |
  | `20`      | Ιδιώτες                                | 0 *                 |

  \* Το `isActive=0` σημαίνει «προσωρινά απενεργοποιημένο στο UI» — τα endpoints δουλεύουν κανονικά και επιστρέφουν δεδομένα. Ο σωστός dropdown πρέπει να καλεί `/gen/getHealthUnitTypes/` δυναμικά.
- **`cDoorId` / `cid`**: Το ID πόρτας/ιατρείου μέσα στο νοσοκομείο (Clinic Door).
- **`groupId`**: Οι ώρες της ημέρας χωρίζονται σε 6 τρίωρα groups: `1=06–09`, `2=09–12`, `3=12–15`, `4=15–18`, `5=18–21`, `6=21–24`.
- **`isOnlyFd`** (0/1): Όταν `1`, η αναζήτηση περιορίζεται σε **Οικογενειακούς/Προσωπικούς Γιατρούς**. Συνδυάζεται με τα ξεχωριστά endpoints `/rv/searchhunitsfd` και `/rv/searchdoctorsfd`.
- **`isMachine`** (0/1): Όταν `1`, ενεργοποιείται **Search by Διαγνωστικό Μηχάνημα** (αξονικός/μαγνητικός/αιματολογικές κ.λπ.). Χρησιμοποιείται με τα `/machines/*` endpoints.
- **`isCovid`** (0/1): Όταν `1`, αναζήτηση εμβολιαστικών κέντρων COVID. Χρησιμοποιεί ξεχωριστή λίστα νομών (`/gen/getprefecturescovid`).
- **`isMentalHealth`** (0/1) + **`rvtypeId=15`**: Mode «Ψυχικής Υγείας». Ξεχωριστή λίστα νομών (`/gen/getprefecturesmentalhealth`).
- **`amka`** (string): ΑΜΚΑ γιατρού. Επιστρέφεται από `/rv/searchdoctors` και χρησιμοποιείται για διαθεσιμότητα συγκεκριμένου ιδιώτη γιατρού.
- **`day`** (0–6): Ημέρα εβδομάδας (Κυριακή=0, Δευτέρα=1, … Σάββατο=6).

---

## 2. Endpoints Ανακάλυψης (Discovery) - `GET` Requests

Αυτά τα endpoints καλούνται με τη μέθοδο `GET` και συνήθως καλό είναι να αποθηκεύονται σε μια cache στον aggregator σου, καθώς σπάνια αλλάζουν. Παρόλα αυτά, χρειάζεται το header `authorization: no-auth`.

### 2.0 Εύρεση τύπων μονάδας υγείας (`getHealthUnitTypes`)
- **Method**: `GET`
- **URL**: `https://www.finddoctors.gov.gr/p-appointment/api/v1/gen/getHealthUnitTypes/` ⚠️ **Trailing slash υποχρεωτικό** — χωρίς αυτό επιστρέφει `404`.
- **Επιστρέφει**: Δυναμική λίστα των τύπων φορέα. Επιβεβαιωμένο live (May 2026):
```json
[
  {"hUnitType":1,  "name":"Νοσοκομείο",                            "isActive":1},
  {"hUnitType":18, "name":"Δημόσιες Δομές",                         "isActive":0},
  {"hUnitType":19, "name":"Ιδιώτες συμβεβλημένοι με τον ΕΟΠΥΥ",     "isActive":0},
  {"hUnitType":20, "name":"Ιδιώτες",                                "isActive":0}
]
```
- **Σχόλιο**: Αυτή είναι η πηγή αλήθειας για τα `foreasID`. Παρόλο που πολλά είναι `isActive:0` στο UI, τα endpoints αναζήτησης δουλεύουν για όλα.

### 2.1 Εύρεση Ειδικοτήτων
- **Method**: `GET`
- **URL**: `https://www.finddoctors.gov.gr/p-appointment/api/v1/gen/getspecialities`
- **Επιστρέφει**: Μια λίστα με όλες τις ειδικότητες για να φτιάξεις το Dropdown του UI σου.
```json
[
  {"speciality": 10, "name": "ΝΕΥΡΟΛΟΓΟΣ", "isActive": 1},
  {"speciality": 12, "name": "ΟΥΡΟΛΟΓΟΣ", "isActive": 1}
]
```

### 2.2 Εύρεση Νομών
- **Method**: `GET`
- **URL**: `https://www.finddoctors.gov.gr/p-appointment/api/v1/gen/getprefectures`
- **Επιστρέφει**: Μια λίστα με τους νομούς της Ελλάδας.

### 2.2.1 Φιλτραρισμένες λίστες νομών (ανά mode)
Για ειδικά modes υπάρχουν περιορισμένες λίστες νομών (επιστρέφουν `404` χωρίς trailing slash):
- **`GET /api/v1/gen/getprefecturescovid`** — νομοί με εμβολιαστικά κέντρα COVID.
- **`GET /api/v1/gen/getprefecturesmentalhealth`** — νομοί με δομές Ψυχικής Υγείας.
- **`POST /api/v1/gen/getfilteredprefectures`** / **`getfilteredspecialities`** — δυναμικά φιλτραρισμένες (επιστρέφουν 500 σε λάθος payload — χρειάζεται περαιτέρω probing).

### 2.2.2 EOPYY notice (`getEOPYYShowMessage`)
- **Method**: `GET`
- **URL**: `https://www.finddoctors.gov.gr/p-appointment/api/v1/gen/getEOPYYShowMessage`
- **Επιστρέφει**: `0` ή `1` (feature flag — δείχνει ένα banner στο UI για το EOPYY mode).

### 2.3 Εύρεση Ιατρείων ανά Νοσοκομείο (Clinic Doors)
- **Method**: `GET`
- **URL**: `https://www.finddoctors.gov.gr/p-appointment/api/v1/gen/cdoorsbyhunitspeciality`
- **Url Params**: `hunitId=21&specialtyId=12&iscovid19=false`
- **Σχόλιο**: Αν θέλουμε να δώσουμε στον χρήστη την επιλογή να διαλέξει συγκεκριμένο "τακτικό εξωτερικό ιατρείο" (π.χ. "Ουρολογικό Ανδρολογικό" vs "Ουρολογικό Συνταγογράφησης"). Προσοχή στις παραμέτρους: εδώ θέλει `hunitId` και `specialtyId`.

---

## 3. Endpoints Αναζήτησης και Διαθεσιμότητας - `POST` Requests

Αυτός είναι ο πυρήνας του συστήματος. Εδώ στέλνεις τα φίλτρα και το σύστημα σου επιστρέφει το πού υπάρχει διαθεσιμότητα. 

### 3.1 Εύρεση Νοσοκομείων που έχουν την ειδικότητα (`searchhunits`)
- **Τι κάνει**: Επιστρέφει όλα τα νοσοκομεία σε ένα νομό (ή και Πανελλαδικά) που εξυπηρετούν την ειδικότητα που ζητάς, μαζί με γεωγραφικές συντεταγμένες (`lattitude`, `longitude`).
- **URL**: `https://www.finddoctors.gov.gr/p-appointment/api/v1/rv/searchhunits`
- **Tip (Εντοπισμός λάθους)**: Εάν βάλεις `prefectureID: null`, επιστρέφει νοσοκομεία από **όλη την Ελλάδα**! 
- **Payload (`dataload`)**:
```json
{
  "startDate": "2026-03-21T00:00:00.000Z",
  "endDate": "2026-07-18T23:59:59.000Z",
  "prefectureID": 5, 
  "specialityID": 12,
  "foreasID": 1,
  "regionalUnitID": null,
  "cDoorId": null,
  "isCovid": 0,
  "isOnlyFd": 0,
  "isMachine": 0
}
```

### 3.1.1 Αναζήτηση Ιδιωτών Γιατρών ονομαστικά (`searchdoctors`) 🆕
- **Τι κάνει**: Επιστρέφει **ονομαστικά γιατρούς** (όχι μονάδες) με ΑΜΚΑ, διεύθυνση, lat/lon, ειδικότητα και (όπου υπάρχει) τηλέφωνο. Είναι το πραγματικό endpoint για τους «Ιδιώτες ΕΟΠΥΥ» (`foreasID=19`) και τους Ιδιώτες (`foreasID=20`).
- **URL**: `POST https://www.finddoctors.gov.gr/p-appointment/api/v1/rv/searchdoctors`
- **Παραλλαγή με location**: `POST /api/v1/rv/searchdoctors/currentlocation` (δέχεται `lattitude`, `longitude`, `distance`).
- **Payload** (επιβεβαιωμένο):
```json
{
  "startDate": "2026-05-20T00:00:00.000Z",
  "endDate":   "2026-08-20T23:59:59.000Z",
  "prefectureID": 5,
  "specialityID": 12,
  "foreasID": 19,
  "isCovid": 0,
  "isOnlyFd": 0,
  "isMachine": 0
}
```
- **Επιστρέφει** (απόσπασμα):
```json
[{
  "firstName":"ΓΕΩΡΓΙΟΣ", "lastName":"ΒΟΥΛΓΑΡΙΔΗΣ",
  "fathersName":"ΔΗΜΗΤΡΙΟΣ", "amka":"09057303092",
  "specialtyName":"ΟΥΡΟΛΟΓΟΣ",
  "address":"ΚΟΛΟΚΟΤΡΩΝΗ 2", "zip":"14232", "cityName":"ΝΕΑ ΙΩΝΙΑ",
  "lattitude":38.03198, "longitude":23.745211,
  "fullName":"ΒΟΥΛΓΑΡΙΔΗΣ ΓΕΩΡΓΙΟΣ, ΔΗΜΗΤΡΙΟΣ"
}]
```
- **Σημείωση**: Αν δεν υπάρχουν γιατροί, γυρνά **ένα** object με `responseCode:2` και όλα τα πεδία `null` — όχι κενό array. Πρέπει να γίνει filter στον client.

### 3.1.2 Αναζήτηση Οικογενειακών Γιατρών (`searchhunitsfd` / `searchdoctorsfd`) 🆕
- **Τι κάνει**: Mode «Προσωπικός/Οικογενειακός Γιατρός» — γυρνά τους εγγεγραμμένους ΠΥ γιατρούς. Ίδια payloads με `searchhunits` / `searchdoctors` αλλά με `isOnlyFd:1`.
- **URLs**:
  - `POST /api/v1/rv/searchhunitsfd` — γυρνά μονάδες-δομές οικογενειακών γιατρών.
  - `POST /api/v1/rv/searchdoctorsfd` — γυρνά ονομαστικά γιατρούς FD.
- **Παρατήρηση**: Στο live API επιστρέφει σχετικά λίγα ή κενά results πανελλαδικά — η ευρετηρίαση είναι μάλλον per-patient (μέσω ΑΜΚΑ). Αν ο χρήστης δεν είναι logged-in, γυρνά placeholder με `responseCode:2`.

### 3.1.3 Αναζήτηση Διαγνωστικών Μηχανημάτων (`machines/searchHunitsMachines`) 🆕
- **Τι κάνει**: Mode «Διαγνωστικά / Εξετάσεις». Αντί για ειδικότητα ζητάει `rvTypeId` (τύπος εξέτασης).
- **URLs**:
  - `GET /api/v1/machines/getMachineRvTypes` — επιστρέφει διαθέσιμους τύπους εξετάσεων.
  - `POST /api/v1/machines/searchHunitsMachines` — αναζήτηση μονάδων με το συγκεκριμένο μηχάνημα.
- **`getMachineRvTypes` response** (επιβεβαιωμένο live):
```json
[
  {"rvTypeId":10,"name":"Αιματολογικές Εξετάσεις","isActive":1,"payType":1,"isMachine":1},
  {"rvTypeId":16,"name":"Απεικονιστικές Εξετάσεις","isActive":1,"payType":1,"isMachine":1}
]
```
- **`payType:1`** σημαίνει ότι η εξέταση χρεώνεται (το UI δείχνει disclaimer).

### 3.2 Το Απόλυτα Γρηγορότερο Ραντεβού (`firstavailableslot`)
- **Τι κάνει**: Αντί να ζητάς το πλήρες "ημερολόγιο" ενός νοσοκομείου, αυτό το endpoint κάνει ένα γρήγορο database query και σου επιστρέφει ΜΟΝΟ ένα String με την ημερομηνία της **πρώτης διαθέσιμης μέρας**. 
- **Χρησιμότητα**: Φανταστικό για να κάνεις loop σε 50 νοσοκομεία και να βρεις το πιο κοντινό ραντεβού σε 2 δευτερόλεπτα!
- **URL**: `https://www.finddoctors.gov.gr/p-appointment/api/v1/rv/firstavailableslot`
- **Payload**:
```json
{
  "startDate": "2026-03-21T00:00:00.000Z",
  "endDate": "2026-07-18T23:59:59.000Z",
  "prefectureID": 5,
  "specialityID": 12,
  "foreasID": 1,
  "hunit": 21
}
```
- **Επιστρέφει**: Ένα απλό string, π.χ. `"2026-04-23"`.

### 3.3 Ο Πίνακας/Πλέγμα Διαθεσιμότητας (`getslotsinit`)
- **Τι κάνει**: Για ένα συγκεκριμένο νοσοκομείο, σου φέρνει το "ημερολόγιο" του μήνα σε γκρουπ των 3 ωρών (τα `groupId` από το 1 ως το 6). 
- **Ανάλυση**: Όταν το `groupColor` επιστρέφει `warning` σημαίνει "Αρκετά διαθέσιμα" και όταν επιστρέφει `danger` σημαίνει "Περιορισμένη διαθεσιμότητα". Αν γράφει `disabled` δεν υπάρχει χώρος ούτε για δείγμα.
- **Tip (Εντοπισμός λάθους)**: Ανάλογα την ειδικότητα, χρειάζεται υποχρεωτικά να περάσεις το `hunit` στο payload.
- **URL**: `https://www.finddoctors.gov.gr/p-appointment/api/v1/rv/getslotsinit`
- **Payload**:
```json
{
  "startDate": "2026-03-26T00:00:00.000Z",
  "endDate": "2026-03-31T23:59:59.000Z",
  "prefectureID": 5,
  "specialityID": 12,
  "foreasID": 1,
  "hunit": 21,
  "regionalUnitID": null,
  "cDoorId": null,
  "isCovid": 0,
  "isOnlyFd": 0,
  "isMachine": 0
}
```

### 3.4 Τα Πραγματικά Ραντεβού (The "Actual Slots") - `getactualslots`
- **Τι κάνει**: Αφού ο χρήστης επιλέξει την "μέρα" (από το `getslotsinit`), αυτό το endpoint καλείται για να φέρει τα ακριβή 10λεπτα ραντεβού (π.χ. "12:20"), την κλινική (cDoorName), τα Σχόλια και φυσικά το **Όνομα του Γιατρού** εάν είναι καταχωρημένο.
- **ΛΑΘΟΣ ΠΟΥ ΚΑΝΑΜΕ ΠΡΙΝ**: Είναι **ΑΠΟΛΥΤΩΣ ΚΡΙΣΙΜΟ** να περνάς το `prefectureId` στο Payload (π.χ. `5`) αλλιώς ο server επιστρέφει `[]`! Επίσης, αν βάλεις `cDoorId: null`, λειτουργεί σωστά επιστρέφοντας slots για **όλα** τα ιατρεία της συγκεκριμένης ειδικότητας μέσα στο νοσοκομείο, αντί να ψάχνεις πόρτα-πόρτα.
- **Επεξήγηση Ημέρας (`day`)**: Προσοχή, το πεδίο `day` δεν είναι η ημερομηνία, είναι η **ημέρα της εβδομάδας** (όπου Κυριακή=0, Δευτέρα=1 ... Σάββατο=6).
- **⚠️ Σημαντική σημασία του `groupColor`** (επαλήθευση 2026):
  - `warning` = αρκετά διαθέσιμα.
  - `danger` = περιορισμένη διαθεσιμότητα.
  - `disabled` = **δεν υπάρχει διαθέσιμο slot ΣΕ ΑΥΤΟ ΤΟ GROUP**. Δεν σημαίνει αναγκαστικά «γεμάτο» — μπορεί απλώς να σημαίνει ότι **ο γιατρός δεν δουλεύει εκείνη τη μέρα/ώρα**. Πολλοί γιατροί έχουν ωράρια Δευ/Τετ/Παρ μόνο.
  - **Συνέπεια για fill-rate calculations**: Το να μετράς `disabled / total` ως «πληρότητα» δίνει ψευδώς υψηλά νούμερα (π.χ. 95% «γεμάτο» όταν ο γιατρός απλά έχει 2 ημέρες/εβδομάδα). Σωστή προσέγγιση: συσχέτισε το `disabled` με ΰπαρξη αντίστοιχου `warning`/`danger` σε άλλη ημέρα της εβδομάδας, ή χρησιμοποίησε ξεχωριστή μετρική `weeklyAvailableDays`. 
- **URL**: `https://www.finddoctors.gov.gr/p-appointment/api/v1/rv/getactualslots`
- **Σωστό Payload**:
```json
{
  "day": 4, 
  "ddate": "2026-03-26T00:00:00.000Z",
  "groupId": 3,
  "hunit": 21,
  "foreas": 1,
  "specialityId": 12,
  "prefectureId": 5,
  "isOnlyFd": 0,
  "isMachine": 0,
  "cDoorId": null
}
```
- **Επιστρέφει (Απόσπασμα)**:
```json
[
  {
    "hUnitId": 21,
    "rvDate": "2026-03-26T12:20:00.000+0200",
    "rvtime": "12:20",
    "doc_name": "ΓΕΝΙΚΟ ΝΟΣΟΚΟΜΕΙΟ ΑΘΗΝΩΝ O ΕΥΑΓΓΕΛΙΣΜΟΣ- ΠΟΛΥΚΛΙΝΙΚΗ:ΟΥΡΟΛΟΓΙΚΟ ΙΑΤΡΕΙΟ - ",
    "address": "ΥΨΗΛΑΝΤΟΥ 45-47",
    "city": "ΑΘΗΝΑ"
  }
]
```
*(Σημείωση: Στο παράδειγμα του Ευαγγελισμού, ενώ επιστρέφει το slot 12:20, το όνομα του γιατρού καταλήγει σε παύλα ` - `, πράγμα που σημαίνει ότι ο Ευαγγελισμός δεν καταχωρεί ονομαστικά τους γιατρούς ανά ραντεβού στα Ουρολογικά της ομάδας 3, παρά μόνο την "Κλινική").*

---

## 4. O Τέλειος Αλγόριθμος για τον δικό μας "Aggregator"

Αν θέλεις να χτίσεις μια εφαρμογή που θα σκίζει, το execution flow που πρέπει να ακολουθήσεις είναι αυτό (συνδυάζοντας παράλληλα τα endpoints):

**Βήμα 1ο - Η Γρήγορη Σάρωση (Triage)**
Αν ο χρήστης ψάχνει για "Ουρολόγο στην Αθήνα", πετάς ΕΝΑ call στο `searchhunits` (για να πάρεις τα 10-20 νοσοκομεία). Μετά πετάς ταυτόχρονα (Παράλληλα Promises) 20 calls στο `firstavailableslot`. 
**Αποτέλεσμα**: Μέσα σε 1 δευτερόλεπτο, έχεις μια λίστα γραμμένη σε κάρτες: *"Ο Ευαγγελισμός έχει ραντεβού σε 5 μέρες, το Λαϊκό σε 10, Το Γεννηματά σε 20"*.

**Βήμα 2ο - Ο Ακριβής Χρόνος (Scope)**
Ο χρήστης κάνει κλικ στην κάρτα του "Ευαγγελισμού". Τοτε η εφαρμογή σου κάνει ένα request στο `getslotsinit` για την ημερομηνία (`2026-03-26`) που βρήκε το Triage. Δείχνεις στον χρήστη "Υπάρχουν ραντεβού μεταξύ 12:00 και 15:00".

**Βήμα 3ο - Η Τελική Επιλογή (Actual)**
Όταν ο χρήστης πατήσει "Δείξε μου τα ραντεβού", καλείς το `getactualslots` βάζοντας **οπωσδήποτε** το `prefectureId` και `cDoorId: null`, και του δείχνεις λίστα:
- 12:20 | ΟΥΡΟΛΟΓΙΚΟ ΙΑΤΡΕΙΟ | ΓΙΑΤΡΟΣ: (Αν υπάρχει)
- 12:40 | ΟΥΡΟΛΟΓΙΚΟ ΑΝΔΡΟΛΟΓΙΚΟ | ΓΙΑΤΡΟΣ: ΚΩΣΤΟΠΟΥΛΟΣ...

**Βήμα 4ο - Κράτηση (Το μοναδικό σημείο πόνου)**
Από τη στιγμή που θα διαλέξει το "12:20", το επόμενο endpoint είναι το `bookappointment` (δεν το αναλύουμε εδώ σε βάθος), ΑΛΛΑ για να γίνει επιτυχώς, πρέπει υποχρεωτικά να στείλεις το χρήστη στη σελίδα Log-in της ΗΔΙΚΑ/TaxisNet για να πάρεις το Cookie `FindDoc` και `cookiesession1`. 
Η βέλτιστη πρακτική θα ήταν αυτή η εμπειρία αναζήτησης (Βήματα 1,2,3) να είναι ορθάνοιχτη, και το login να γίνεται ακριβώς **τη στιγμή της κράτησης**. 

---

## 4b. Έξτρα modes (επιβεβαιωμένα από reverse-engineering του frontend, May 2026)

Στο επίσημο UI υπάρχουν 6 διακριτές «καρτέλες» αναζήτησης που ο aggregator πρέπει να καλύπτει:

| Mode στο UI                | foreasID / rvtypeId           | Flags                          | Endpoints                                                       |
|----------------------------|-------------------------------|--------------------------------|-----------------------------------------------------------------|
| Δημόσια Νοσοκομεία (ΕΣΥ)    | `foreasID=1`                  | `isOnlyFd=0`                   | `/rv/searchhunits` + `/rv/firstavailableslot`                   |
| Δημόσιες Δομές (ΠΦΥ)        | `foreasID=18`                 | `isOnlyFd=0`                   | `/rv/searchhunits` + `/rv/firstavailableslot`                   |
| Ιδιώτες συμβεβλημένοι ΕΟΠΥΥ | `foreasID=19`                 | `isOnlyFd=0`                   | `/rv/searchdoctors` + `/rv/searchdoctors/currentlocation`       |
| Ιδιώτες                     | `foreasID=20`                 | `isOnlyFd=0`                   | `/rv/searchdoctors`                                             |
| Προσωπικός/Οικογενειακός Γιατρός | `foreasID=18`            | `isOnlyFd=1`                   | `/rv/searchhunitsfd` + `/rv/searchdoctorsfd`                    |
| Ψυχικής Υγείας              | `rvtypeId=15`                 | `isMentalHealth=1`             | `/rv/searchhunits` + ξεχωριστή λίστα νομών                      |
| Εμβολιασμοί COVID           | —                             | `isCovid=1`                    | `/rv/searchhunits` + `/gen/getprefecturescovid`                 |
| Διαγνωστικά Μηχανήματα      | —                             | `isMachine=1`, `rvTypeId=10/16` | `/machines/getMachineRvTypes` + `/machines/searchHunitsMachines` |

**Σημαντικό**: Ο τρέχων δικός μας aggregator καλύπτει **μόνο τις 2 πρώτες γραμμές**. Οι υπόλοιπες 6 είναι gaps που πρέπει να καλυφθούν (βλ. issues #8–#15).

### Πλήρης λίστα endpoints (από reverse-engineering του Angular bundle `5-es2015.*.js`)

**Discovery (GET, με trailing slash όπου σημειώνεται)**:
- `/api/v1/gen/getspecialities`
- `/api/v1/gen/getprefectures`
- `/api/v1/gen/getprefecturescovid`
- `/api/v1/gen/getprefecturesmentalhealth`
- `/api/v1/gen/getHealthUnitTypes/` ⚠️
- `/api/v1/gen/cdoorsbyhunitspeciality`
- `/api/v1/gen/getEOPYYShowMessage`
- `/api/v1/gen/getcity`, `/getmunicipality`, `/getregionalunit` (πιθανώς POST — επιστρέφουν 404 σε GET)
- `/api/v1/gen/getfilteredspecialities` (POST, payload-dependent)
- `/api/v1/gen/getfilteredprefectures` (POST, payload-dependent)
- `/api/v1/gen/cancelation`
- `/api/v1/gen/getparapompesbyamka`, `/updateparapompistatus` (παραπομπές — απαιτεί ΑΜΚΑ + login)

**Αναζήτηση & διαθεσιμότητα (POST)**:
- `/api/v1/rv/searchhunits`
- `/api/v1/rv/searchhunitsfd`
- `/api/v1/rv/searchdoctors`
- `/api/v1/rv/searchdoctors/currentlocation`
- `/api/v1/rv/searchdoctorsfd`
- `/api/v1/rv/firstavailableslot`
- `/api/v1/rv/getslotsinit`
- `/api/v1/rv/getslots` (variant — να ερευνηθεί)
- `/api/v1/rv/getactualslots`
- `/api/v1/rv/getDoctorDetails` (επιστρέφει 404 σε quick probe — πιθανώς GET με query)

**Booking (POST, απαιτούν cookie)**:
- `/api/v1/rv/bookrvwithhunit`
- `/api/v1/rv/bookrvwithouthunit`
- `/api/v1/rv/cancelrv`
- `/api/v1/rv/getmyrvs`
- `/api/v1/rv/getRvDetails`
- `/api/v1/rv/rvpdf`

**Machines (POST/GET)**:
- `/api/v1/machines/getMachineRvTypes`
- `/api/v1/machines/searchHunitsMachines`

**Patient management**:
- `/api/v1/patienterv/getPatientRvs`, `/savedetails`, `/generatepin`, `/verifypin`, `/generateCredentials`, `/cancelRv`

**Auth / Geo**:
- `/api/v1/auth/taxisnet`, `/auth/logout`
- `/api/v1/geocoding/map` (Mapbox proxy μάλλον)

---

## 5. Προηγμένα Σενάρια Χρήσης & Αρχιτεκτονική Backend

Εφόσον το API είναι δημόσιο, ο ιδανικός τρόπος για να το αξιοποιήσεις είναι να χτίσεις έναν **δικό σου Backend Server (π.χ. σε Golang)**. Το δικό σου backend θα λειτουργεί ως "Έξυπνος Μεσολαβητής" (API Wrapper / Aggregator), ο οποίος θα δέχεται απλά, user-friendly requests από το frontend σου (χωρίς να ασχολείσαι καθόλου με εσωτερικά IDs όπως 12 και 21), και στο παρασκήνιο θα εκτελεί παράλληλα (Concurrent) HTTP requests, επιστρέφοντας καθαρά κι επεξεργασμένα δεδομένα.

### Γιατί Golang; (Concurrency for Max Speed)
Με τα *Goroutines* της Golang, μπορείς να τρέξεις 50-100 HTTP requests ταυτόχρονα προς το `firstavailableslot` χωρίς να καθυστερήσεις ούτε δευτερόλεπτο. Το Υπουργείο δεν φαίνεται να έχει αυστηρό rate-limiting (και αν έχει, μπορεί να ξεπεραστεί εύκολα με backoff ή caching). Το δικό σου response θα έρχεται σε ελάχιστα δευτερόλεπτα, ενώ ένας χρήστης στο επίσημο site θα έκανε κυριολεκτικά πάνω 1 ώρα να κάνει κλικ-κλικ σε κάθε νοσοκομείο.

### Ιδανικά "Σύνθετα" Endpoints του Δικού σου Server:

#### Σενάριο Α: Το "Emergency Finder" (Ψάχνω γιατρό ΧΘΕΣ)
- **Το Endpoint Σου**: `GET /api/emergency?specialtyId=23&lat=37.9&lng=23.7`
- **Τι κάνει ο Server σου**: 
  1. Λαμβάνει απευθείας το ID (όχι hardcoded μεταφράσεις, το UI σου το έχει πάρει από τη δυναμική λίστα των ειδικοτήτων).
  2. Καλεί το `searchhunits` για να πάρει όλα τα κοντινά νοσοκομεία.
  3. Φιλτράρει / Ταξινομεί τα 5 πιο κοντινά νοσοκομεία συγκρίνοντας τα δικά σου `lat/lng` με αυτά που επιστρέφει το API.
  4. Ανοίγει **5 παράλληλα goroutines** προς το `firstavailableslot`.
  5. Συγκρίνει τις ημερομηνίες και επιστρέφει στον χρήστη **ΜΟΝΟ** το νοσοκομείο που εχει διαθέσιμο ραντεβού **σήμερα ή αύριο**.

#### Σενάριο Β: The "Nationwide Specialist" Hunt (Η βελόνα στ' άχυρα)
- **Το Endpoint Σου**: `GET /api/nationwide?specialtyId=10` (όπου 10=Νευρολόγος, το ID περνάει δυναμικά από το UI).
- **Τι κάνει ο Server σου**: 
  1. Καλεί το `searchhunits` με `prefectureID: null` (Μυστική Πανελλαδική σάρωση).
  2. Ανακαλύπτει τα (π.χ. 12) νοσοκομεία σε όλη τη χώρα που έχουν Νευροχειρουργό.
  3. Ανοίγει **12 goroutines** χτυπώντας το `firstavailableslot` για το καθένα.
  4. Ταξινομεί τα αποτελέσματα βάσει της πιο σύντομης ημερομηνίας.
  5. Επιστρέφει μια έτοιμη, ταξινομημένη λίστα στο Frontend: *"Βρήκαμε 1 στην Αθήνα σε 3 μήνες, 1 στην Κρήτη σε 2 εβδομάδες και 1 στα Γρεβενά αύριο! Διαλέγεις και παίρνεις."*

#### Σενάριο Γ: Hospital Heatmap / Capacity Dashboard
- **Το Endpoint Σου**: `GET /api/hospitals/21/capacity` (π.χ. Ευαγγελισμός)
- **Τι κάνει ο Server σου**: 
  1. Έχει ήδη αποθηκευμένη (cached) τη λίστα με τις ειδικότητες από το `/gen/getspecialities`.
  2. Ανοίγει δεκάδες **goroutines**, ένα για κάθε ειδικότητα, καλώντας το `getslotsinit` για την τρέχουσα εβδομάδα.
  3. Καταμετράει πόσα slots επέστρεψαν `danger` (ελεύθερα) και πόσα `disabled` (κλεισμένα).
  4. Επιστρέφει ένα στατιστικό report (π.χ. "Ο Ευαγγελισμός έχει 98% πληρότητα αυτή την εβδομάδα, αλλά η Ουρολογική έχει κενά"). Το απόλυτο εργαλείο για open-data dashboards!

#### Σενάριο Δ: Ο "Σκύλος-Φύλακας" Ακυρώσεων (Cancellation Watchdog)
- **Το Endpoint Σου**: `POST /api/watchdog` (Ο χρήστης δηλώνει *"Ειδοποίησέ με αν αδειάσει Παθολόγος στο Λαϊκό"*).
- **Τι κάνει ο Server σου**: 
  1. Ένας background worker (cron / ticker) στην Go κάνει poll (π.χ. κάθε 10 λεπτά) το `firstavailableslot` για τον Παθολόγο στο Λαϊκό, από την IP του server σου.
  2. Αν το string της ημερομηνίας που επιστρέφει το API γίνει *νωρίτερο* από την "γνωστή" κλεισμένη του χρήστη, το backend σου "ξυπνάει".
  3. Στέλνει ένα Push Notification, Email ή SMS (μέσω webhook): *"Έλα γρήγορα! Μόλις ακυρώθηκε ραντεβού στο Λαϊκό για αύριο στις 10:00! Κλείστο τώρα."*

### Συνοψίζοντας: Zero-Maintenance Architecture (Χωρίς Hardcoded Data)
Έχεις απόλυτο δίκιο ότι η δημιουργία δικών μας "Ονομάτων" (π.χ. `/api/specialty/urologos`) εγκυμονεί τον **κίνδυνο βλάβης (breaking change)** εάν το Υπουργείο αλλάξει κάτι, σβήσει ένα ID ή προσθέσει νέες ειδικότητες. Για να πετύχεις 100% Type-Safe & Zero-Maintenance αρχιτεκτονική:

1. 
  Το Frontend (UI)  και ο server --αν χρειαστεί-- χρησιμοποιεί το `/gen/getspecialities` του Υπουργείου για να "διαβάζει" δυναμικά τις διαθέσιμες ειδικότητες και τους κωδικούς τους. Τα ονόματα είναι ήδη αρκετά περιγραφικά και απολύτως έγκυρα.
2. **Pass-Through IDs**:
   Tο UI σου γεμίζει τα Dropdowns του με βάση τα ορίτζιναλ IDs (`12`, `5`, `21`). Όταν ο χρήστης πατάει αναζήτηση, το Frontend στέλνει αυτά τα IDs πίσω στον Backend Server σου (π.χ. `/api/emergency?specialtyId=12`).
3. **Ο Server είναι Αγνοήμων (Agnostic)**:
   Ο Aggregator σου στην Golang δεν έχει **καμία ιδέα** για το αν το `12` σημαίνει Ουρολόγος ή Οδοντίατρος. Το μόνο που τον νοιάζει είναι να πάρει τον αριθμό (ID), να ανοίξει τα goroutines και να χτυπήσει τα API του συστήματος, επιστρέφοντας πίσω ταχύτατα αποτελέσματα.

Αυτή είναι η **μόνη βιώσιμη λύση** για παραγωγικό (production) σύστημα που δεν θα "σπάσει" ποτέ και δεν θα χρειάζεται manual commits στο Source Code κάθε φορά που ανοίγει ένα νέο νοσοκομείο ή προστίθεται μια ιατρική υπηρεσία.

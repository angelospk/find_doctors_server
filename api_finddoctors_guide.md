# Οδηγός του API του finddoctors.gov.gr

Ο οδηγός τεκμηριώνει, με reverse engineering, το API που χρησιμοποιεί το Υπουργείο Υγείας για τα ηλεκτρονικά ραντεβού σε νοσοκομεία και μονάδες ΠΦΥ (Πρωτοβάθμιας Φροντίδας Υγείας).

**Πρόσβαση:** Κάθε endpoint του Υπουργείου θέλει ενεργό TaxisNet session (cookie `JSESSIONID`). Χωρίς αυτό απαντά `403 {"error":"Not found"}` (επαληθεύτηκε στις 2026-10-20 από 3 διευθύνσεις IP). Ο server κάνει μόνος του login στο TaxisNet και ανανεώνει το session.

---

## 1. Βασικές έννοιες και παράμετροι

Το API κάνει διάκριση πεζών-κεφαλαίων και δεν ονομάζει τις παραμέτρους με συνέπεια (CamelCase ή όχι). Κάθε κλειδί πρέπει να γράφεται ακριβώς όπως το θέλει το συγκεκριμένο endpoint (π.χ. αλλού `specialityID` και αλλού `specialityId`).

- **`prefectureID` / `prefectureId`**: Ο κωδικός του νομού (π.χ. 5 = Αττική, 11 = Έβρος). Σε κάποια endpoints το `null` δίνει πανελλαδική αναζήτηση.
- **`specialityID` / `specialtyId` / `spec`**: Ο εσωτερικός κωδικός της ειδικότητας (π.χ. 10 = Νευρολόγος, 12 = Ουρολόγος).
- **`hunit` / `hunitId`**: Το ID της μονάδας υγείας (νοσοκομείο ή κέντρο υγείας). Π.χ. 21 = Ευαγγελισμός, 718 = Π.Γ.Ν. Αλεξανδρούπολης.
- **`foreasID` / `foreas` / `hUnitType`**: Ο τύπος του φορέα. Η πλήρης λίστα (από `/gen/getHealthUnitTypes/`, επιβεβαιωμένη live):
  | hUnitType | name                                  | isActive (May 2026) |
  |-----------|---------------------------------------|---------------------|
  | `1`       | Νοσοκομείο (ΕΣΥ)                       | 1                   |
  | `18`      | Δημόσιες Δομές (ΠΦΥ / Κέντρα Υγείας)   | 0 *                 |
  | `19`      | Ιδιώτες συμβεβλημένοι με ΕΟΠΥΥ         | 0 *                 |
  | `20`      | Ιδιώτες                                | 0 *                 |

  \* Το `isActive=0` σημαίνει ότι η επιλογή είναι προσωρινά κρυμμένη στο UI. Τα endpoints δουλεύουν κανονικά και επιστρέφουν δεδομένα. Ένα dropdown τύπων πρέπει να διαβάζει το `/gen/getHealthUnitTypes/` δυναμικά.
- **`cDoorId` / `cid`**: Το ID του ιατρείου («πόρτας», clinic door) μέσα στο νοσοκομείο.
- **`groupId`**: Η ημέρα χωρίζεται σε 6 τρίωρα groups: `1=06–09`, `2=09–12`, `3=12–15`, `4=15–18`, `5=18–21`, `6=21–24`.
- **`isOnlyFd`** (0/1): Με `1` η αναζήτηση περιορίζεται σε οικογενειακούς/προσωπικούς γιατρούς. Πάει μαζί με τα ξεχωριστά endpoints `/rv/searchhunitsfd` και `/rv/searchdoctorsfd`.
- **`isMachine`** (0/1): Με `1` η αναζήτηση γίνεται ανά διαγνωστικό μηχάνημα (αξονικός, μαγνητικός, αιματολογικές κ.λπ.). Χρησιμοποιείται με τα endpoints `/machines/*`.
- **`isCovid`** (0/1): Με `1` αναζητούνται εμβολιαστικά κέντρα COVID. Έχει δική του λίστα νομών (`/gen/getprefecturescovid`).
- **`isMentalHealth`** (0/1) + **`rvtypeId=15`**: Η αναζήτηση «Ψυχικής Υγείας». Έχει δική της λίστα νομών (`/gen/getprefecturesmentalhealth`).
- **`amka`** (string): Το ΑΜΚΑ του γιατρού. Το επιστρέφει το `/rv/searchdoctors` και χρειάζεται για τη διαθεσιμότητα συγκεκριμένου ιδιώτη γιατρού.
- **`day`** (0–6): Ημέρα της εβδομάδας (Κυριακή=0, Δευτέρα=1, … Σάββατο=6).

---

## 2. Endpoints ανακάλυψης (discovery), `GET`

Αυτά τα endpoints καλούνται με `GET`. Τα δεδομένα τους αλλάζουν σπάνια, οπότε συμφέρει να μένουν σε cache. Στέλνουν και αυτά το header `authorization: no-auth`, μαζί με το session cookie.

### 2.0 Τύποι μονάδας υγείας (`getHealthUnitTypes`)
- **Method**: `GET`
- **URL**: `https://www.finddoctors.gov.gr/p-appointment/api/v1/gen/getHealthUnitTypes/` (το τελικό `/` είναι υποχρεωτικό· χωρίς αυτό απαντά `404`).
- **Επιστρέφει**: Τη λίστα των τύπων φορέα. Επιβεβαιωμένο live (May 2026):
```json
[
  {"hUnitType":1,  "name":"Νοσοκομείο",                            "isActive":1},
  {"hUnitType":18, "name":"Δημόσιες Δομές",                         "isActive":0},
  {"hUnitType":19, "name":"Ιδιώτες συμβεβλημένοι με τον ΕΟΠΥΥ",     "isActive":0},
  {"hUnitType":20, "name":"Ιδιώτες",                                "isActive":0}
]
```
- **Σχόλιο**: Αυτή είναι η πηγή για τα `foreasID`. Πολλά είναι `isActive:0` στο UI, αλλά τα endpoints αναζήτησης δουλεύουν για όλα.

### 2.1 Ειδικότητες
- **Method**: `GET`
- **URL**: `https://www.finddoctors.gov.gr/p-appointment/api/v1/gen/getspecialities`
- **Επιστρέφει**: Όλες τις ειδικότητες, για το dropdown του UI.
```json
[
  {"speciality": 10, "name": "ΝΕΥΡΟΛΟΓΟΣ", "isActive": 1},
  {"speciality": 12, "name": "ΟΥΡΟΛΟΓΟΣ", "isActive": 1}
]
```

### 2.2 Νομοί
- **Method**: `GET`
- **URL**: `https://www.finddoctors.gov.gr/p-appointment/api/v1/gen/getprefectures`
- **Επιστρέφει**: Τους νομούς της Ελλάδας.

### 2.2.1 Λίστες νομών ανά τύπο αναζήτησης
Κάποιοι τύποι αναζήτησης έχουν δική τους, μικρότερη λίστα νομών (χωρίς τελικό `/` απαντούν `404`):
- **`GET /api/v1/gen/getprefecturescovid`**: νομοί με εμβολιαστικά κέντρα COVID.
- **`GET /api/v1/gen/getprefecturesmentalhealth`**: νομοί με δομές Ψυχικής Υγείας.
- **`POST /api/v1/gen/getfilteredprefectures`** / **`getfilteredspecialities`**: φιλτράρουν δυναμικά. Με λάθος payload απαντούν 500· το σωστό payload δεν έχει βρεθεί ακόμα.

### 2.2.2 Ειδοποίηση ΕΟΠΥΥ (`getEOPYYShowMessage`)
- **Method**: `GET`
- **URL**: `https://www.finddoctors.gov.gr/p-appointment/api/v1/gen/getEOPYYShowMessage`
- **Επιστρέφει**: `0` ή `1`. Είναι feature flag: με `1` το UI δείχνει ένα banner στην αναζήτηση ΕΟΠΥΥ.

### 2.3 Ιατρεία ανά νοσοκομείο (clinic doors)
- **Method**: `GET`
- **URL**: `https://www.finddoctors.gov.gr/p-appointment/api/v1/gen/cdoorsbyhunitspeciality`
- **Url Params**: `hunitId=21&specialtyId=12&iscovid19=false`
- **Σχόλιο**: Χρησιμεύει όταν ο χρήστης πρέπει να διαλέξει συγκεκριμένο τακτικό εξωτερικό ιατρείο (π.χ. «Ουρολογικό Ανδρολογικό» ή «Ουρολογικό Συνταγογράφησης»). Εδώ τα ονόματα είναι `hunitId` και `specialtyId`. Με `specialityID` το Υπουργείο απαντά `400` (επαληθεύτηκε στις 2026-10-21).

---

## 3. Endpoints αναζήτησης και διαθεσιμότητας, `POST`

Σε αυτά τα endpoints στέλνονται τα φίλτρα, και το Υπουργείο απαντά πού υπάρχει διαθεσιμότητα.

### 3.1 Νοσοκομεία με την ειδικότητα (`searchhunits`)
- **Τι κάνει**: Επιστρέφει όλα τα νοσοκομεία ενός νομού (ή όλης της χώρας) που έχουν την ειδικότητα, με συντεταγμένες (`lattitude`, `longitude`).
- **URL**: `https://www.finddoctors.gov.gr/p-appointment/api/v1/rv/searchhunits`
- **Πανελλαδικά**: Με `prefectureID: null` επιστρέφει νοσοκομεία από όλη την Ελλάδα.
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

### 3.1.1 Ιδιώτες γιατροί ονομαστικά (`searchdoctors`)
- **Τι κάνει**: Επιστρέφει γιατρούς ονομαστικά (όχι μονάδες), με ΑΜΚΑ, διεύθυνση, lat/lon, ειδικότητα και, όπου υπάρχει, τηλέφωνο. Αυτό είναι το endpoint για τους «Ιδιώτες ΕΟΠΥΥ» (`foreasID=19`) και τους ιδιώτες (`foreasID=20`).
- **URL**: `POST https://www.finddoctors.gov.gr/p-appointment/api/v1/rv/searchdoctors`
- **Παραλλαγή με τοποθεσία**: `POST /api/v1/rv/searchdoctors/currentlocation` (δέχεται `lattitude`, `longitude`, `distance`).
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
  "firstName":"ΙΩΑΝΝΗΣ", "lastName":"ΠΑΠΑΔΟΠΟΥΛΟΣ",
  "fathersName":"ΝΙΚΟΛΑΟΣ", "amka":"11111111111",
  "specialtyName":"ΟΥΡΟΛΟΓΟΣ",
  "address":"ΚΟΛΟΚΟΤΡΩΝΗ 2", "zip":"14232", "cityName":"ΝΕΑ ΙΩΝΙΑ",
  "lattitude":38.03198, "longitude":23.745211,
  "fullName":"ΠΑΠΑΔΟΠΟΥΛΟΣ ΙΩΑΝΝΗΣ, ΝΙΚΟΛΑΟΣ"
}]
```
- **Σημείωση**: Όταν δεν υπάρχουν γιατροί, η απάντηση δεν είναι κενό array αλλά **ένα** object με `responseCode:2` και όλα τα πεδία `null`. Ο client πρέπει να το φιλτράρει.

### 3.1.2 Οικογενειακοί γιατροί (`searchhunitsfd` / `searchdoctorsfd`)
- **Τι κάνει**: Η αναζήτηση «Προσωπικός/Οικογενειακός Γιατρός». Επιστρέφει τους εγγεγραμμένους γιατρούς ΠΦΥ. Τα payloads είναι ίδια με του `searchhunits` / `searchdoctors`, με `isOnlyFd:1`.
- **URLs**:
  - `POST /api/v1/rv/searchhunitsfd`: μονάδες οικογενειακών γιατρών.
  - `POST /api/v1/rv/searchdoctorsfd`: οικογενειακοί γιατροί ονομαστικά.
- **Παρατήρηση**: Πανελλαδικά το live API επιστρέφει λίγα ή καθόλου αποτελέσματα. Μάλλον τα αποτελέσματα εξαρτώνται από τον ασθενή (μέσω ΑΜΚΑ). Χωρίς login η απάντηση είναι placeholder με `responseCode:2`.

### 3.1.3 Διαγνωστικά μηχανήματα (`machines/searchHunitsMachines`)
- **Τι κάνει**: Η αναζήτηση «Διαγνωστικά / Εξετάσεις». Αντί για ειδικότητα δέχεται `rvTypeId` (τύπος εξέτασης).
- **URLs**:
  - `GET /api/v1/machines/getMachineRvTypes`: οι διαθέσιμοι τύποι εξετάσεων.
  - `POST /api/v1/machines/searchHunitsMachines`: μονάδες με το συγκεκριμένο μηχάνημα.
- **Απάντηση του `getMachineRvTypes`** (επιβεβαιωμένη live):
```json
[
  {"rvTypeId":10,"name":"Αιματολογικές Εξετάσεις","isActive":1,"payType":1,"isMachine":1},
  {"rvTypeId":16,"name":"Απεικονιστικές Εξετάσεις","isActive":1,"payType":1,"isMachine":1}
]
```
- **`payType:1`** σημαίνει ότι η εξέταση χρεώνεται (το UI δείχνει σχετική προειδοποίηση).

### 3.2 Πρώτη διαθέσιμη ημέρα (`firstavailableslot`)
- **Τι κάνει**: Επιστρέφει μόνο ένα string, την ημερομηνία της πρώτης διαθέσιμης ημέρας, χωρίς όλο το ημερολόγιο του νοσοκομείου.
- **Χρήση**: Με παράλληλες κλήσεις σε πολλά νοσοκομεία βρίσκεται ποιο έχει το πιο κοντινό ραντεβού, χωρίς να ζητηθεί το ημερολόγιο κανενός.
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
- **Επιστρέφει**: Ένα string, π.χ. `"2026-04-23"`.

### 3.3 Πλέγμα διαθεσιμότητας (`getslotsinit`)
- **Τι κάνει**: Για ένα νοσοκομείο, επιστρέφει το ημερολόγιο σε τρίωρα groups (`groupId` 1 έως 6). Το `groupColor` κάθε group εξηγείται στην §3.4.
- **Το `hunit` στο payload**: Για κάποιες ειδικότητες είναι υποχρεωτικό.
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

### 3.4 Τα ραντεβού μιας ημέρας (`getactualslots`)
- **Τι κάνει**: Αφού επιλεγεί η ημέρα (από το `getslotsinit`), επιστρέφει τα ακριβή δεκάλεπτα ραντεβού (π.χ. «12:20»), το ιατρείο (`cDoorName`), τα σχόλια και το όνομα του γιατρού, όταν το έχει καταχωρίσει η μονάδα.
- **Το `prefectureId` είναι υποχρεωτικό** στο payload (π.χ. `5`). Χωρίς αυτό η απάντηση είναι `[]`. Με `cDoorId: null` επιστρέφονται τα slots όλων των ιατρείων της ειδικότητας στο νοσοκομείο, χωρίς ερώτηση ανά ιατρείο.
- **Το πεδίο `day`** δεν είναι ημερομηνία αλλά ημέρα της εβδομάδας (Κυριακή=0, Δευτέρα=1 … Σάββατο=6).
- **Η σημασία του `groupColor`** (επαλήθευση 2026):
  - `warning` = αρκετά διαθέσιμα.
  - `danger` = περιορισμένη διαθεσιμότητα.
  - `disabled` = δεν υπάρχει διαθέσιμο slot σε αυτό το group. Αυτό δεν σημαίνει απαραίτητα ότι είναι γεμάτο. Συχνά ο γιατρός απλώς δεν δουλεύει εκείνη την ημέρα ή ώρα· πολλοί δουλεύουν μόνο Δευτέρα, Τετάρτη και Παρασκευή.
  - **Συνέπεια για τον υπολογισμό πληρότητας**: Ο λόγος `disabled / total` δίνει ψεύτικα υψηλά νούμερα (π.χ. 95% «γεμάτο», ενώ ο γιατρός απλώς δουλεύει 2 ημέρες την εβδομάδα). Το `disabled` μετράει μόνο αν την ίδια ημέρα της εβδομάδας υπάρχει `warning`/`danger` κάποια άλλη εβδομάδα· εναλλακτικά χρειάζεται ξεχωριστή μέτρηση, π.χ. `weeklyAvailableDays`.
- **URL**: `https://www.finddoctors.gov.gr/p-appointment/api/v1/rv/getactualslots`
- **Payload**:
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
- **Επιστρέφει (απόσπασμα)**:
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
*(Στο παράδειγμα του Ευαγγελισμού το slot 12:20 υπάρχει, αλλά το όνομα του γιατρού καταλήγει σε ` - `. Ο Ευαγγελισμός δηλαδή δεν καταχωρίζει γιατρό ανά ραντεβού στα ουρολογικά ιατρεία του group 3, μόνο το ιατρείο.)*

---

## 4. Η ροή μιας αναζήτησης

**Βήμα 1: γρήγορη σάρωση**
Για «Ουρολόγο στην Αθήνα», μία κλήση στο `searchhunits` φέρνει τα 10-20 νοσοκομεία. Ακολουθούν 20 παράλληλες κλήσεις στο `firstavailableslot`.
**Αποτέλεσμα**: μια λίστα όπως *«Ευαγγελισμός σε 5 ημέρες, Λαϊκό σε 10, Γεννηματάς σε 20»*.

**Βήμα 2: οι ώρες της ημέρας**
Ο χρήστης επιλέγει τον Ευαγγελισμό. Μία κλήση στο `getslotsinit` για την ημερομηνία που βρήκε το βήμα 1 (`2026-03-26`) δείχνει, π.χ., «υπάρχουν ραντεβού μεταξύ 12:00 και 15:00».

**Βήμα 3: τα ραντεβού**
Μετά καλείται το `getactualslots`, με `prefectureId` και `cDoorId: null`. Η λίστα μοιάζει έτσι:
- 12:20 | ΟΥΡΟΛΟΓΙΚΟ ΙΑΤΡΕΙΟ | ΓΙΑΤΡΟΣ: (αν υπάρχει)
- 12:40 | ΟΥΡΟΛΟΓΙΚΟ ΑΝΔΡΟΛΟΓΙΚΟ | ΓΙΑΤΡΟΣ: ΚΩΣΤΟΠΟΥΛΟΣ...

**Βήμα 4: κράτηση**
Η κράτηση γίνεται με το `bookappointment` (δεν αναλύεται εδώ). Θέλει login του χρήστη στην ΗΔΙΚΑ/TaxisNet, που δίνει τα cookies `FindDoc` και `cookiesession1`. Το login χρειάζεται μόνο τη στιγμή της κράτησης· η αναζήτηση (βήματα 1 έως 3) δεν το χρειάζεται από την πλευρά του χρήστη.

---

## 4b. Οι τύποι αναζήτησης του επίσημου UI (από reverse engineering του frontend, May 2026)

Το επίσημο UI έχει τους εξής τύπους αναζήτησης:

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

Ο server καλύπτει όλους αυτούς τους τύπους (βλ. `README.md`). Το `searchdoctors/currentlocation` δεν έχει δώσει ποτέ αποτέλεσμα.

### Πλήρης λίστα endpoints (από reverse engineering του Angular bundle `5-es2015.*.js`)

**Discovery (GET, με τελικό `/` όπου σημειώνεται)**:
- `/api/v1/gen/getspecialities`
- `/api/v1/gen/getprefectures`
- `/api/v1/gen/getprefecturescovid`
- `/api/v1/gen/getprefecturesmentalhealth`
- `/api/v1/gen/getHealthUnitTypes/` (με τελικό `/`)
- `/api/v1/gen/cdoorsbyhunitspeciality`
- `/api/v1/gen/getEOPYYShowMessage`
- `/api/v1/gen/getcity`, `/getmunicipality`, `/getregionalunit` (πιθανώς POST· σε GET απαντούν 404)
- `/api/v1/gen/getfilteredspecialities` (POST, εξαρτάται από το payload)
- `/api/v1/gen/getfilteredprefectures` (POST, εξαρτάται από το payload)
- `/api/v1/gen/cancelation`
- `/api/v1/gen/getparapompesbyamka`, `/updateparapompistatus` (παραπομπές· θέλουν ΑΜΚΑ και login)

**Αναζήτηση και διαθεσιμότητα (POST)**:
- `/api/v1/rv/searchhunits`
- `/api/v1/rv/searchhunitsfd`
- `/api/v1/rv/searchdoctors`
- `/api/v1/rv/searchdoctors/currentlocation`
- `/api/v1/rv/searchdoctorsfd`
- `/api/v1/rv/firstavailableslot`
- `/api/v1/rv/getslotsinit`
- `/api/v1/rv/getslots` (παραλλαγή· δεν έχει ερευνηθεί)
- `/api/v1/rv/getactualslots`
- `/api/v1/rv/getDoctorDetails` (σε γρήγορο probe απάντησε 404· πιθανώς GET με query)

**Κράτηση (POST, θέλουν cookie)**:
- `/api/v1/rv/bookrvwithhunit`
- `/api/v1/rv/bookrvwithouthunit`
- `/api/v1/rv/cancelrv`
- `/api/v1/rv/getmyrvs`
- `/api/v1/rv/getRvDetails`
- `/api/v1/rv/rvpdf`

**Machines (POST/GET)**:
- `/api/v1/machines/getMachineRvTypes`
- `/api/v1/machines/searchHunitsMachines`

**Διαχείριση ασθενή**:
- `/api/v1/patienterv/getPatientRvs`, `/savedetails`, `/generatepin`, `/verifypin`, `/generateCredentials`, `/cancelRv`

**Auth / Geo**:
- `/api/v1/auth/taxisnet`, `/auth/logout`
- `/api/v1/geocoding/map` (μάλλον proxy για Mapbox)

---

## 5. Πώς τα χρησιμοποιεί ο server

Ο server (Go) δέχεται απλά requests και κάνει από πίσω τις κλήσεις στο Υπουργείο, παράλληλα. Τα endpoints του περιγράφονται στο `README.md`. Οι κύριες χρήσεις:

- **Αναζήτηση** (`GET /api/search`): `searchhunits` για ΕΣΥ και ΠΦΥ παράλληλα, μετά `firstavailableslot` για κάθε μονάδα μέσα στην ακτίνα, ταξινόμηση με το συντομότερο ραντεβού. Καλύπτει και την αναζήτηση «κοντά μου» και την πανελλαδική (`prefectureID: null`).
- **Πληρότητα** (`GET /api/hospitals/{hunitId}/capacity`, `GET /api/heatmap`): `getslotsinit` ανά ειδικότητα, με τον κανόνα της §3.4 για το `disabled`.
- **Ειδοποίηση για νωρίτερη ημερομηνία** (`POST /api/watches`): ένας background worker ξαναρωτά το `firstavailableslot` ανά διάστημα και ειδοποιεί όταν εμφανιστεί νωρίτερη ημερομηνία.

Ο server δεν κρατά δικά του ονόματα ή αντιστοιχίες για ειδικότητες και νομούς. Το UI διαβάζει τα IDs από το `/gen/getspecialities` και τα υπόλοιπα discovery endpoints, και ο server τα περνά αυτούσια στο Υπουργείο. Μια νέα ειδικότητα εμφανίζεται έτσι χωρίς αλλαγή στον κώδικα.

Ο server στέλνει το πολύ 30 ταυτόχρονες κλήσεις στο Υπουργείο (`MINISTRY_MAX_CONCURRENCY`), ρωτά από προεπιλογή έως 60 μονάδες ανά αναζήτηση (`UNIT_PROBE_LIMIT`· το `maxProbes` το αλλάζει, με ανώτατο όριο 1000) και κρατά τα discovery δεδομένα σε cache. Η εβδομαδιαία σάρωση του καταλόγου κάνει παύση τουλάχιστον 1,5 s ανάμεσα στα αιτήματα.

/**
 * HUnit represents a single Health Unit returned by the /rv/searchhunits endpoint.
 * It includes all metadata fields discovered during traffic analysis.
 */
export interface HUnit {
  hunitId: any; // Polymorphic: string for PFY, number for Hospitals
  hunit: number | null;
  hunittype: number | null;
  name: string;
  city: string;
  zip: string;
  phone1: string;
  phone2?: string;
  address: string;
  lattitude: number; // Note: API uses 'lattitude' with double 't'
  longitude: number;
  region?: number;
  prefecture?: number;
  isactive?: number;
  clinics: any[]; // List of clinics/departments
  responseCode: number;
}

/**
 * Specialty represents a medical specialty metadata as returned by /gen/getspecialities.
 */
export interface Specialty {
  speciality: number;
  name: string;
}

/**
 * Slot represents an available appointment time window.
 */
export interface Slot {
  startTime: string;
  endTime: string;
  isFree: boolean;
}

/**
 * DaySlots represents a collection of slots for a specific date.
 */
export interface DaySlots {
  date: string;
  slots: Slot[];
}

/**
 * SearchPayload represents the JSON body format required by research and availability endpoints.
 */
export interface SearchPayload {
  startDate: string;
  endDate: string;
  prefectureID: number | null;
  specialityID: number;
  foreasID: number;
  hunit?: number;
  cDoorId?: number;
  isCovid: number;
  isOnlyFd: number;
  isMachine: number;
}

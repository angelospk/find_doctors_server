import { HUnit } from "./ministry";

/**
 * ScannedUnit represents a health unit with its earliest available appointment date.
 */
export interface ScannedUnit extends HUnit {
  firstDate: string;
}

export interface SpecialtyCapacity {
  specialityId: number;
  name: string;
  fillRate: number; // Percentage of "disabled" slots
}

export interface CapacityReport {
  hunitId: number;
  scanned: number;
  specialties: SpecialtyCapacity[];
}

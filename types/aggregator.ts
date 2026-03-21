import { HUnit } from "./ministry";

/**
 * ScannedUnit represents a health unit with its earliest available appointment date.
 */
export interface ScannedUnit extends HUnit {
  firstDate: string;
}

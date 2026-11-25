import { describe, expect, it } from 'vitest';
import { yamlBlockIndent } from './yamlIndent';
describe('YAML block indentation', () => {
 it('opens root, nested and sequence mapping blocks with two-space indentation', () => {
  expect(yamlBlockIndent('headers:')).toBe('  ');
  expect(yamlBlockIndent('  customResponseHeaders:')).toBe('    ');
  expect(yamlBlockIndent('  - headers:')).toBe('      ');
 });
 it('handles quoted keys and comments without indenting ordinary values or comment lines', () => {
  expect(yamlBlockIndent('"custom-key": # note')).toBe('  ');
  expect(yamlBlockIndent('  frameDeny: true')).toBeNull();
  expect(yamlBlockIndent('# headers:')).toBeNull();
  expect(yamlBlockIndent('note: example # colon:')).toBeNull();
 });
});

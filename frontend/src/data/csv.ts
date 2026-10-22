/** Encode exported text for spreadsheet readers as well as CSV parsers. */
export function csvTextCell(value: string): string {
  const text = /^\s*[=+\-@]/u.test(value) || /^[\t\r\n]/u.test(value) ? `'${value}` : value;
  return `"${text.replaceAll('"', '""')}"`;
}

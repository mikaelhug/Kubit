const csvField = (s: string) => (/[",\r\n]/.test(s) ? `"${s.replace(/"/g, '""')}"` : s)

export const csvLine = (fields: string[]) => fields.map(csvField).join(',')

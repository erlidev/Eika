/** formatScheduleTime shows an occurrence in its schedule's time zone. */
export function formatScheduleTime(value: string | null, timeZone: string): string {
  if (!value) return "None";
  return new Intl.DateTimeFormat(undefined, {
    timeZone,
    timeZoneName: "short",
    year: "numeric",
    month: "numeric",
    day: "numeric",
    hour: "numeric",
    minute: "2-digit",
  }).format(new Date(value));
}

// Builds the Gmail search filter for a scan request from the form's options.

export type GmailFilterOptions = {
  inbox: boolean;
  unread: boolean;
  // YYYY-MM-DD, as <input type="date"> gives it, or "" when not set.
  startDate: string;
  endDate: string;
};

// Formats a YYYY-MM-DD date as Gmail's YYYY/MM/DD, shifted by `days`.
export function dateForApi(input: string, days = 0): string {
  const [year, month, day] = input.split("-").map(Number);
  const date = new Date(Date.UTC(year, month - 1, day + days));
  return date.toISOString().slice(0, 10).replace(/-/g, "/");
}

export function buildGmailFilter({
  inbox,
  unread,
  startDate,
  endDate,
}: GmailFilterOptions): string {
  const terms: string[] = [];
  if (inbox) {
    terms.push("label:inbox");
  }
  if (unread) {
    terms.push("is:unread");
  }
  if (startDate !== "") {
    terms.push(`after:${dateForApi(startDate)}`);
  }
  if (endDate !== "") {
    // Gmail's before: is exclusive; use the next day to include endDate.
    terms.push(`before:${dateForApi(endDate, 1)}`);
  }
  return terms.join(" ");
}

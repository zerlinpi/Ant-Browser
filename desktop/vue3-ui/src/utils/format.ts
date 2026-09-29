const dateTimeFormatter = new Intl.DateTimeFormat("zh-CN", { dateStyle: "short", timeStyle: "short" });
const fullDateTimeFormatter = new Intl.DateTimeFormat("zh-CN", { dateStyle: "medium", timeStyle: "medium" });

const parse = (value?: string | null) => {
  if (!value) return undefined;
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? undefined : date;
};

export const formatDateTime = (value?: string | null) => {
  const date = parse(value);
  return date ? dateTimeFormatter.format(date) : "—";
};

export const formatFullDateTime = (value?: string | null) => {
  const date = parse(value);
  return date ? fullDateTimeFormatter.format(date) : "—";
};

export const shortId = (value?: string | null) => {
  if (!value) return "—";
  return value.length > 12 ? `${value.slice(0, 8)}…` : value;
};

export const copyToClipboard = async (text: string): Promise<boolean> => {
  try {
    await navigator.clipboard.writeText(text);
    return true;
  } catch {
    // Embedded webviews may deny the async clipboard API; fall back to a
    // transient selection that is removed immediately.
    const area = document.createElement("textarea");
    area.value = text;
    area.setAttribute("readonly", "");
    area.style.position = "fixed";
    area.style.opacity = "0";
    document.body.appendChild(area);
    area.select();
    try {
      return document.execCommand("copy");
    } catch {
      return false;
    } finally {
      area.remove();
    }
  }
};

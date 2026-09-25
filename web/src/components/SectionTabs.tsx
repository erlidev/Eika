/**
 * A row of tabs that switches between the parts of one page, such as an MCP
 * server's page or a profile's editor. It is a plain tablist rather than the
 * Tabs primitive, whose vertical styling the settings dialog's own tab column
 * would impose on any tabs inside it.
 */

import { nextTabIndex } from "@/lib/tablist";
import { cn } from "@/lib/utils";

/** SectionTab is one tab: its id, its title, and a count shown beside it. */
export type SectionTab<T extends string> = { id: T; title: string; count?: number };

export type SectionTabsProps<T extends string> = {
  /** idPrefix names the tabs' ids and their panels': `<prefix>-tab-<id>`, `<prefix>-section-<id>`. */
  idPrefix: string;
  /** label names the tablist for a screen reader. */
  label: string;
  tabs: SectionTab<T>[];
  value: T;
  onChange: (section: T) => void;
};

export function SectionTabs<T extends string>({
  idPrefix,
  label,
  tabs,
  value,
  onChange,
}: SectionTabsProps<T>) {
  const onKeyDown = (e: React.KeyboardEvent, from: number) => {
    const to = nextTabIndex(from, tabs.length, e.key);
    const next = to === null ? undefined : tabs[to];
    if (!next) return;
    e.preventDefault();
    onChange(next.id);
    document.getElementById(`${idPrefix}-tab-${next.id}`)?.focus();
  };
  return (
    <div
      role="tablist"
      aria-label={label}
      className="flex items-center gap-0.5 overflow-x-auto border-b pb-1"
    >
      {tabs.map((tab, index) => (
        <button
          key={tab.id}
          type="button"
          role="tab"
          id={`${idPrefix}-tab-${tab.id}`}
          aria-selected={value === tab.id}
          aria-controls={`${idPrefix}-section-${tab.id}`}
          tabIndex={value === tab.id ? 0 : -1}
          className={cn(
            "hover:bg-accent focus-visible:ring-ring flex shrink-0 items-center gap-1.5 rounded-md px-2 py-1 text-xs transition-colors focus-visible:ring-1 focus-visible:outline-none",
            value === tab.id && "bg-accent font-medium",
          )}
          onClick={() => {
            onChange(tab.id);
          }}
          onKeyDown={(e) => {
            onKeyDown(e, index);
          }}
        >
          {tab.title}
          {tab.count !== undefined && (
            <span aria-hidden className="text-muted-foreground font-mono tabular-nums">
              {tab.count}
            </span>
          )}
        </button>
      ))}
    </div>
  );
}

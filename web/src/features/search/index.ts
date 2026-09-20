/** The search feature: the search queries and the rules behind the search settings. */
export {
  keyLabels,
  limitProblem,
  maxLimit,
  moveProvider,
  parseLimit,
  readLimits,
  readOrder,
  safeHref,
  searchSettingKeys,
  toggleProvider,
  usageText,
} from "@/features/search/search";
export type { LimitField } from "@/features/search/search";
export { useSaveSearchKey, useSearchStatus, useTrySearch } from "@/features/search/queries";

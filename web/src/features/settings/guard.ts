import { useQuery } from "@tanstack/react-query";
import { queryKeys } from "@/api/keys";
import { getGuard } from "@/api/routes";

export function useGuard() {
  return useQuery({
    queryKey: queryKeys.guard(),
    queryFn: ({ signal }) => getGuard(signal),
    refetchInterval: 10000,
  });
}

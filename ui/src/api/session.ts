import { QueryClient } from "@tanstack/react-query";
import { User } from ".";
import { queryKeys } from "./queryKeys";

/**
 * Records who is logged in (null: nobody) and drops everything cached for
 * whoever was before. The session entry is updated in place, not removed, so
 * components already watching it (the header) see the change.
 */
export function setSession(queryClient: QueryClient, user: User | null) {
  queryClient.removeQueries({
    predicate: (query) => query.queryKey[0] !== queryKeys.me[0],
  });
  queryClient.setQueryData(queryKeys.me, user);
}

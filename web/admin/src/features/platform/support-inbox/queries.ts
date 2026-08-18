import { useQuery, useMutation, useQueryClient } from "@tanstack/react-query";
import * as api from "./api";
import type { ListTicketsParams, SupportTicketStatus } from "./contract";

const LIST_KEY = ["platform", "support", "tickets"] as const;

function keyFor(params: ListTicketsParams): readonly unknown[] {
  return [
    ...LIST_KEY,
    "list",
    params.limit ?? 50,
    params.offset ?? 0,
    params.status ?? "",
    params.category ?? "",
    params.tenant_id ?? "",
    params.search ?? "",
  ] as const;
}

export function useTickets(params: ListTicketsParams = {}) {
  return useQuery({
    queryKey: keyFor(params),
    queryFn: () => api.listTickets(params),
    staleTime: 5_000,
  });
}

const DETAIL_KEY = ["platform", "support", "ticket"] as const;

function detailKey(ref: string): readonly unknown[] {
  return [...DETAIL_KEY, ref] as const;
}

export function useTicketDetail(ref: string | null) {
  return useQuery({
    queryKey: ref ? detailKey(ref) : [...DETAIL_KEY, "empty"],
    queryFn: () => api.getTicket(ref as string),
    enabled: !!ref,
  });
}

export function useTicketReply() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ ref, body }: { ref: string; body: string }) => api.replyOnTicket(ref, body),
    onSuccess: (_data, vars) => {
      void qc.invalidateQueries({ queryKey: detailKey(vars.ref) });
      void qc.invalidateQueries({ queryKey: [...LIST_KEY, "list"] });
    },
  });
}

export function useTicketStatusChange() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ ref, status }: { ref: string; status: SupportTicketStatus }) =>
      api.setTicketStatus(ref, status),
    onSuccess: (_data, vars) => {
      void qc.invalidateQueries({ queryKey: detailKey(vars.ref) });
      void qc.invalidateQueries({ queryKey: [...LIST_KEY, "list"] });
    },
  });
}

import { describe, expect, it, vi, beforeEach } from "vitest";
import { renderHook, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { createElement, type ReactNode } from "react";
import { useSetTenantScope, useClearTenantScope, useTenantScope } from "./queries";
import { request } from "../../../api";

vi.mock("../../../api", () => ({
  request: vi.fn(),
}));

const mockedRequest = vi.mocked(request);

function makeWrapper(qc: QueryClient) {
  return ({ children }: { children: ReactNode }) =>
    createElement(QueryClientProvider, { client: qc }, children);
}

describe("platform tenant scope isolation", () => {
  beforeEach(() => {
    mockedRequest.mockReset();
  });

  it("starts with no tenant scope and never fabricates a tenant", async () => {
    const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    const { result } = renderHook(() => useTenantScope(), { wrapper: makeWrapper(qc) });
    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    expect(result.current.data?.tenantId).toBeNull();
  });

  it("setting a scope evicts every tenant-scoped mail query", async () => {
    const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    qc.setQueryData(["platform-domains", "list", 7], { domains: [] });
    qc.setQueryData(["platform-mailboxes", "list", 7], { mailboxes: [] });
    qc.setQueryData(["platform-aliases", "list", 7], { aliases: [] });
    qc.setQueryData(["platform-groups", "list", 7], { groups: [] });
    qc.setQueryData(["platform-suppressions", "list", 7], { suppressions: [] });

    const { result } = renderHook(() => useSetTenantScope(), { wrapper: makeWrapper(qc) });

    result.current.mutate({ tenantId: 7, tenantName: "Acme" });
    await waitFor(() => expect(result.current.isSuccess).toBe(true));

    expect(qc.getQueryData(["platform-domains", "list", 7])).toBeUndefined();
    expect(qc.getQueryData(["platform-mailboxes", "list", 7])).toBeUndefined();
    expect(qc.getQueryData(["platform-aliases", "list", 7])).toBeUndefined();
    expect(qc.getQueryData(["platform-groups", "list", 7])).toBeUndefined();
    expect(qc.getQueryData(["platform-suppressions", "list", 7])).toBeUndefined();
  });

  it("clearing the scope evicts tenant-scoped queries too", async () => {
    const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    qc.setQueryData(["platform-mailboxes", "list", 7], { mailboxes: [] });
    const { result } = renderHook(() => useClearTenantScope(), { wrapper: makeWrapper(qc) });
    result.current.mutate();
    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    expect(qc.getQueryData(["platform-mailboxes", "list", 7])).toBeUndefined();
  });

  it("the scope state is a pure client selection: no network call and no tenant route", async () => {
    const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    const { result } = renderHook(() => useTenantScope(), { wrapper: makeWrapper(qc) });
    await waitFor(() => expect(result.current.isSuccess).toBe(true));
    expect(mockedRequest).not.toHaveBeenCalled();
  });
});

import "@testing-library/jest-dom/vitest";
import { describe, it, expect, afterEach, vi } from "vitest";
import { render, screen, cleanup, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import ClusterPanel from "./ClusterPanel";
import * as api from "../api";

function renderPanel() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(<QueryClientProvider client={qc}><ClusterPanel /></QueryClientProvider>);
}

describe("Reliability > ClusterPanel", () => {
  afterEach(() => { cleanup(); vi.restoreAllMocks(); });

  it("renders the real single-node deployment status honestly, never implying a cluster that doesn't exist", async () => {
    vi.spyOn(api, "getClusterStatus").mockResolvedValue({
      deployment_mode: "single_node", current_nodes: 1, max_nodes: 1, consensus: "absent", peer_nodes: [],
      proxies: [{ name: "imap_proxy", kind: "imap", configured: false, runtime_state: "absent", detail: "single-node deployment" }],
      honest_note: "Orvix Enterprise currently runs as a single-node deployment. Clustering + proxy replication is not implemented in this build.",
    });
    renderPanel();
    await waitFor(() => expect(screen.getByText("single_node")).toBeInTheDocument());
    expect(screen.getByText(/clustering \+ proxy replication is not implemented/i)).toBeInTheDocument();
    expect(screen.queryByText(/"deployment_mode"/)).not.toBeInTheDocument();
  });
});

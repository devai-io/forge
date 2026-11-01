import { QueryClient, QueryClientProvider, useQuery } from "@tanstack/react-query";
import { act, fireEvent, render, screen } from "@testing-library/react";
import { PullToRefresh } from "@/features/shell/PullToRefresh";
import { canPullFrom } from "@/lib/pull";

function Page({ fetcher }: { fetcher: () => Promise<string> }) {
  const q = useQuery({ queryKey: ["page"], queryFn: fetcher });
  return (
    <div>
      <p>{q.data ?? "…"}</p>
      <input aria-label="Search" />
      <div data-testid="list" style={{ overflowY: "auto" }}>
        <span>row</span>
      </div>
    </div>
  );
}

function setup() {
  let n = 0;
  const fetcher = vi.fn(async () => `load ${++n}`);
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false, staleTime: Infinity } } });
  render(
    <QueryClientProvider client={qc}>
      <Page fetcher={fetcher} />
      <PullToRefresh />
    </QueryClientProvider>,
  );
  return fetcher;
}

const pull = (el: Element, from: number, to: number, dx = 0) => {
  fireEvent.touchStart(el, { touches: [{ clientX: 100, clientY: from }] });
  fireEvent.touchMove(el, { touches: [{ clientX: 100 + dx / 2, clientY: from + (to - from) / 2 }] });
  fireEvent.touchMove(el, { touches: [{ clientX: 100 + dx, clientY: to }] });
};

describe("pull to refresh", () => {
  beforeEach(() => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    Object.defineProperty(window, "scrollY", { value: 0, configurable: true });
  });
  afterEach(() => vi.useRealTimers());

  it("refetches the page's data when pulled past the mark", async () => {
    const fetcher = setup();
    expect(await screen.findByText("load 1")).toBeInTheDocument();
    const page = screen.getByText("load 1");

    pull(page, 100, 140); // 40px of finger = 20px of pull: not enough
    expect(screen.getByRole("status", { name: "Pull to refresh" })).toBeInTheDocument();
    fireEvent.touchEnd(page);
    expect(screen.queryByRole("status")).not.toBeInTheDocument();
    expect(fetcher).toHaveBeenCalledTimes(1);

    pull(page, 100, 300);
    expect(screen.getByRole("status", { name: "Release to refresh" })).toBeInTheDocument();
    fireEvent.touchEnd(page);
    expect(screen.getByRole("status", { name: "Refreshing" })).toBeInTheDocument();
    expect(await screen.findByText("load 2")).toBeInTheDocument();
    await act(async () => {
      await vi.advanceTimersByTimeAsync(600);
    });
    expect(screen.queryByRole("status")).not.toBeInTheDocument();
    expect(fetcher).toHaveBeenCalledTimes(2);
  });

  it("does not fire away from the top, sideways, or from a field", async () => {
    const fetcher = setup();
    await screen.findByText("load 1");
    const page = screen.getByText("load 1");

    pull(page, 100, 300, 400); // mostly sideways
    fireEvent.touchEnd(page);
    pull(screen.getByLabelText("Search"), 100, 300);
    fireEvent.touchEnd(page);
    Object.defineProperty(window, "scrollY", { value: 120, configurable: true });
    pull(page, 100, 300);
    fireEvent.touchEnd(page);

    await act(async () => {
      await vi.advanceTimersByTimeAsync(600);
    });
    expect(screen.queryByRole("status")).not.toBeInTheDocument();
    expect(fetcher).toHaveBeenCalledTimes(1);
  });

  it("leaves a scrolled inner list and open dialogs alone", () => {
    const list = document.createElement("div");
    list.style.overflowY = "auto";
    const row = document.createElement("span");
    list.appendChild(row);
    document.body.appendChild(list);
    expect(canPullFrom(row)).toBe(true);
    list.scrollTop = 50;
    Object.defineProperty(list, "scrollTop", { value: 50, configurable: true });
    expect(canPullFrom(row)).toBe(false);

    const dialog = document.createElement("div");
    dialog.setAttribute("aria-modal", "true");
    document.body.appendChild(dialog);
    expect(canPullFrom(document.body.firstElementChild)).toBe(false);
    dialog.remove();
    list.remove();
  });
});

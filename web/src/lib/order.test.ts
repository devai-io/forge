import { orderAtEnd, orderAtStart, orderForDrop, sortOrderBetween } from "./order";

const col = (...orders: number[]) => orders.map((sort_order, i) => ({ id: i + 1, sort_order }));

describe("sortOrderBetween", () => {
  it("takes the midpoint between two neighbours", () => {
    expect(sortOrderBetween(1000, 2000)).toBe(1500);
  });
  it("steps 1000 past the last card and before the first", () => {
    expect(sortOrderBetween(3000, undefined)).toBe(4000);
    expect(sortOrderBetween(undefined, 1000)).toBe(0);
  });
  it("starts an empty column at 1000", () => {
    expect(sortOrderBetween()).toBe(1000);
  });
});

describe("orderForDrop", () => {
  it("drops into another column between two cards", () => {
    expect(orderForDrop(col(1000, 2000, 3000), 99, 1)).toBe(1500);
  });
  it("drops at the top and bottom of another column", () => {
    expect(orderForDrop(col(1000, 2000), 99, 0)).toBe(0);
    expect(orderForDrop(col(1000, 2000), 99, 2)).toBe(3000);
  });
  it("moves a card down within its own column", () => {
    // Card 1 (1000) dropped in the slot after card 3 (index 3).
    expect(orderForDrop(col(1000, 2000, 3000), 1, 3)).toBe(4000);
    // Card 1 dropped between card 2 and 3 (slot 2).
    expect(orderForDrop(col(1000, 2000, 3000), 1, 2)).toBe(2500);
  });
  it("moves a card up within its own column", () => {
    expect(orderForDrop(col(1000, 2000, 3000), 3, 0)).toBe(0);
    expect(orderForDrop(col(1000, 2000, 3000), 3, 1)).toBe(1500);
  });
  it("treats dropping a card where it already is as a no-op", () => {
    expect(orderForDrop(col(1000, 2000, 3000), 2, 1)).toBeNull();
    expect(orderForDrop(col(1000, 2000, 3000), 2, 2)).toBeNull();
  });
  it("handles an empty column", () => {
    expect(orderForDrop([], 5, 0)).toBe(1000);
  });
});

describe("orderAtEnd / orderAtStart", () => {
  it("steps past the extremes regardless of array order", () => {
    expect(orderAtEnd(col(3000, 1000))).toBe(4000);
    expect(orderAtStart(col(3000, 1000))).toBe(0);
    expect(orderAtEnd([])).toBe(1000);
  });
});

// Kanban ordering. Tasks carry a float `sort_order`, ascending within a
// column; a move sends one number instead of renumbering the column. The
// contract's rule: midpoint between the new neighbours, last + 1000 at the
// bottom, first − 1000 at the top, 1000 in an empty column.

export const ORDER_STEP = 1000;

export function sortOrderBetween(before?: number | null, after?: number | null): number {
  const hasBefore = before !== undefined && before !== null;
  const hasAfter = after !== undefined && after !== null;
  if (hasBefore && hasAfter) return (before + after) / 2;
  if (hasBefore) return before + ORDER_STEP;
  if (hasAfter) return after - ORDER_STEP;
  return ORDER_STEP;
}

/**
 * The sort_order for dropping `draggedId` at `index` of a column (the column's
 * items in display order, which may include the dragged item itself when it is
 * moved within its own column). Returns null when the drop is a no-op.
 */
export function orderForDrop<T extends { id: number; sort_order: number }>(
  column: T[],
  draggedId: number,
  index: number,
): number | null {
  const currentIndex = column.findIndex((t) => t.id === draggedId);
  if (currentIndex !== -1 && (index === currentIndex || index === currentIndex + 1)) return null;
  const others = column.filter((t) => t.id !== draggedId);
  // Removing the dragged item shifts every later slot up by one.
  const target = currentIndex !== -1 && index > currentIndex ? index - 1 : index;
  const clamped = Math.max(0, Math.min(target, others.length));
  const before = others[clamped - 1]?.sort_order;
  const after = others[clamped]?.sort_order;
  return sortOrderBetween(before, after);
}

/** Order for appending to the bottom of a column. */
export function orderAtEnd(column: { sort_order: number }[]): number {
  if (!column.length) return ORDER_STEP;
  return Math.max(...column.map((t) => t.sort_order)) + ORDER_STEP;
}

/** Order for inserting at the top of a column. */
export function orderAtStart(column: { sort_order: number }[]): number {
  if (!column.length) return ORDER_STEP;
  return Math.min(...column.map((t) => t.sort_order)) - ORDER_STEP;
}

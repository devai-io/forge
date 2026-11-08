// Path display helpers.

/** /home/<user>/dev/x → ~/dev/x (and /Users/<user> on a Mac): the prefix is the same on every row. */
export function shortHome(dir: string): string {
  return dir.replace(/^\/(?:home|Users)\/[^/]+(?=\/|$)/, "~");
}

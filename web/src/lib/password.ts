// A rough strength hint for the password forms. Not a policy: the API enforces
// the only rule (12+ characters); this just nudges towards longer passphrases.

export type Strength = { score: 0 | 1 | 2 | 3 | 4; label: string };

const LABELS = ["too short", "weak", "fair", "good", "strong"] as const;

export function passwordStrength(pw: string): Strength {
  if (pw.length < 12) return { score: 0, label: LABELS[0] };
  const classes = [/[a-z]/, /[A-Z]/, /\d/, /[^A-Za-z0-9]/].filter((re) => re.test(pw)).length;
  const unique = new Set(pw).size;
  let score = 1;
  if (pw.length >= 14) score++;
  if (pw.length >= 20) score++;
  if (classes >= 3) score++;
  if (unique < pw.length / 3) score = Math.max(1, score - 2); // "aaaaaaaaaaaaaa"
  const clamped = Math.min(4, score) as Strength["score"];
  return { score: clamped, label: LABELS[clamped] };
}

// Kalah, the common form of mancala: six pits a side with four seeds each.
//
// Pits 0–5 are player 0's (South, left to right), 6 is South's store,
// 7–12 are North's pits (right to left as South sees them) and 13 is
// North's store. A player lifts all seeds from one of their pits and sows
// them one by one counter-clockwise, skipping the opponent's store. Ending
// in one's own store gives another turn. Ending in one's own empty pit
// captures that seed and the seeds in the opposite pit, if there are any.
// When one side's pits are empty, the other player stores what is left on
// their side, and the fuller store wins.

export const players = ['South', 'North'];
export const STORE = [6, 13];

export function newGame(opts = {}) {
  const seeds = opts.seeds || 4;
  const pits = new Array(14).fill(seeds);
  pits[6] = 0;
  pits[13] = 0;
  return { pits, side: 0, over: false, prev: null, last: null };
}

export function turn(s) { return s.side; }
const own = (side, i) => (side === 0 ? i >= 0 && i <= 5 : i >= 7 && i <= 12);
export const opposite = (i) => 12 - i;

export function moves(s) {
  if (s._moves) return s._moves;
  if (s.over) return (s._moves = []);
  const out = [];
  const base = s.side === 0 ? 0 : 7;
  for (let i = base; i < base + 6; i++) if (s.pits[i] > 0) out.push({ at: i });
  return (s._moves = out);
}

export function play(s, m) {
  if (!moves(s).some((x) => x.at === m.at)) throw new Error('Choose one of your own pits that has seeds.');
  const pits = s.pits.slice();
  let n = pits[m.at];
  pits[m.at] = 0;
  let i = m.at;
  while (n > 0) {
    i = (i + 1) % 14;
    if (i === STORE[1 - s.side]) continue;
    pits[i]++;
    n--;
  }
  let captured = 0;
  if (own(s.side, i) && pits[i] === 1 && pits[opposite(i)] > 0) {
    captured = pits[opposite(i)] + 1;
    pits[STORE[s.side]] += captured;
    pits[i] = 0;
    pits[opposite(i)] = 0;
  }
  const again = i === STORE[s.side];
  const southEmpty = pits.slice(0, 6).every((v) => v === 0);
  const northEmpty = pits.slice(7, 13).every((v) => v === 0);
  let over = false;
  if (southEmpty || northEmpty) {
    for (let k = 0; k < 6; k++) { pits[6] += pits[k]; pits[k] = 0; }
    for (let k = 7; k < 13; k++) { pits[13] += pits[k]; pits[k] = 0; }
    over = true;
  }
  return { pits, side: over || again ? s.side : 1 - s.side, over, prev: s,
    last: { at: m.at, end: i, again: again && !over, captured } };
}

export function result(s) {
  if (!s.over) return null;
  const a = s.pits[6];
  const b = s.pits[13];
  return { winner: a === b ? null : a > b ? 0 : 1, reason: `${a}–${b}` };
}

export function evaluate(s, player) {
  const mine = s.pits[STORE[player]] - s.pits[STORE[1 - player]];
  let side = 0;
  for (let k = 0; k < 6; k++) side += s.pits[k] - s.pits[k + 7];
  return 20 * mine + (player === 0 ? side : -side);
}

// Moves that end in the store first: they give another turn.
export function order(s, list) {
  const base = s.side === 0 ? 0 : 7;
  const target = (m) => (s.pits[m.at] === STORE[s.side] - m.at ? 1 : 0);
  return list.slice().sort((a, b) => target(b) - target(a) || (b.at - base) - (a.at - base));
}

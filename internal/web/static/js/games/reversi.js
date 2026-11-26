// Reversi on an 8×8 board. Squares are row * 8 + column. Player 0 (Black)
// moves first. A disc must be placed so that it closes at least one line of
// the opponent's discs, which are turned over. A player with no such move
// passes; the game ends when neither can move, and the most discs win.

export const players = ['Black', 'White'];
const DIRS = [[-1, -1], [-1, 0], [-1, 1], [0, -1], [0, 1], [1, -1], [1, 0], [1, 1]];

export function newGame() {
  const board = new Array(64).fill(null);
  // White on the top-left and bottom-right centre squares, as usual.
  board[27] = 1; board[36] = 1;
  board[28] = 0; board[35] = 0;
  return { board, side: 0, passes: 0, prev: null, last: null };
}

export function turn(s) { return s.side; }

function flips(board, at, side) {
  if (board[at] !== null) return [];
  const out = [];
  const r0 = at >> 3;
  const c0 = at & 7;
  for (const [dr, dc] of DIRS) {
    const line = [];
    let r = r0 + dr;
    let c = c0 + dc;
    while (r >= 0 && r < 8 && c >= 0 && c < 8 && board[r * 8 + c] === 1 - side) {
      line.push(r * 8 + c);
      r += dr; c += dc;
    }
    if (line.length && r >= 0 && r < 8 && c >= 0 && c < 8 && board[r * 8 + c] === side) out.push(...line);
  }
  return out;
}

function placements(board, side) {
  const out = [];
  for (let at = 0; at < 64; at++) {
    const f = flips(board, at, side);
    if (f.length) out.push({ at, flips: f });
  }
  return out;
}

export function moves(s) {
  if (s._moves) return s._moves;
  if (s.passes >= 2) return (s._moves = []);
  const list = placements(s.board, s.side);
  // No placement: pass, unless the other player cannot move either.
  s._moves = list.length ? list : placements(s.board, 1 - s.side).length ? [{ pass: true }] : [];
  return s._moves;
}

export function play(s, m) {
  if (m.pass) {
    if (!moves(s).some((x) => x.pass)) throw new Error('You have a move, so you cannot pass.');
    return { board: s.board, side: 1 - s.side, passes: s.passes + 1, prev: s, last: m };
  }
  const legal = moves(s).find((x) => x.at === m.at);
  if (!legal) throw new Error('A disc there would not turn any over.');
  const board = s.board.slice();
  board[legal.at] = s.side;
  for (const f of legal.flips) board[f] = s.side;
  return { board, side: 1 - s.side, passes: 0, prev: s, last: legal };
}

export function count(s) {
  const n = [0, 0];
  for (const v of s.board) if (v !== null) n[v]++;
  return n;
}

export function result(s) {
  if (moves(s).length) return null;
  const [b, w] = count(s);
  return { winner: b === w ? null : b > w ? 0 : 1, reason: `${b}–${w}` };
}

// Corners are worth most; the squares next to an empty corner give it away.
const WEIGHT = [
  100, -20, 10, 5, 5, 10, -20, 100,
  -20, -50, -2, -2, -2, -2, -50, -20,
  10, -2, 1, 1, 1, 1, -2, 10,
  5, -2, 1, 0, 0, 1, -2, 5,
  5, -2, 1, 0, 0, 1, -2, 5,
  10, -2, 1, 1, 1, 1, -2, 10,
  -20, -50, -2, -2, -2, -2, -50, -20,
  100, -20, 10, 5, 5, 10, -20, 100,
];

export function evaluate(s, player) {
  let pos = 0;
  let discs = 0;
  for (let i = 0; i < 64; i++) {
    const v = s.board[i];
    if (v === null) continue;
    discs++;
    pos += v === player ? WEIGHT[i] : -WEIGHT[i];
  }
  const mobility = placements(s.board, player).length - placements(s.board, 1 - player).length;
  if (discs > 54) {
    // Near the end only the count matters.
    const n = count(s);
    return 10 * (n[player] - n[1 - player]) + pos;
  }
  return pos + 5 * mobility;
}

export function order(s, list) {
  return list.slice().sort((a, b) => (b.at === undefined ? 0 : WEIGHT[b.at]) - (a.at === undefined ? 0 : WEIGHT[a.at]));
}

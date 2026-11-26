// Checkers (English draughts) on an 8×8 board.
//
// Squares are row * 8 + column, row 0 at the top. Only dark squares, where
// row + column is odd, are used. Player 0 (Dark) starts on rows 5 to 7 and
// moves up the board, and moves first; player 1 (Light) starts on rows 0 to
// 2 and moves down. Captures are compulsory and a capturing piece must keep
// jumping while it can. A man that reaches the far row is crowned and its
// move ends there. A player who cannot move loses. The game is drawn after
// 40 moves by each player without a capture or a man moving.

export const players = ['Dark', 'Light'];

// Pieces: 'd' / 'l' men, 'D' / 'L' kings.
export function newGame() {
  const board = new Array(64).fill(null);
  for (let r = 0; r < 8; r++) {
    for (let c = 0; c < 8; c++) {
      if ((r + c) % 2 === 0) continue;
      if (r <= 2) board[r * 8 + c] = 'l';
      if (r >= 5) board[r * 8 + c] = 'd';
    }
  }
  return { board, side: 0, quiet: 0, prev: null, last: null };
}

const owner = (p) => (p === 'd' || p === 'D' ? 0 : 1);
const isKing = (p) => p === 'D' || p === 'L';

export function turn(s) { return s.side; }

function dirs(p) {
  if (isKing(p)) return [[-1, -1], [-1, 1], [1, -1], [1, 1]];
  return owner(p) === 0 ? [[-1, -1], [-1, 1]] : [[1, -1], [1, 1]];
}

const inside = (r, c) => r >= 0 && r < 8 && c >= 0 && c < 8;

// jumps extends a capture sequence from sq; a man crowned on the far row
// stops there.
function jumps(board, sq, piece, path, captured, out) {
  const r = sq >> 3;
  const c = sq & 7;
  let extended = false;
  const crowned = !isKing(piece) && path.length > 1 && (owner(piece) === 0 ? r === 0 : r === 7);
  if (!crowned) {
    for (const [dr, dc] of dirs(piece)) {
      const mr = r + dr;
      const mc = c + dc;
      const tr = r + 2 * dr;
      const tc = c + 2 * dc;
      if (!inside(tr, tc)) continue;
      const mid = mr * 8 + mc;
      const to = tr * 8 + tc;
      const victim = board[mid];
      if (!victim || owner(victim) === owner(piece) || captured.includes(mid) || board[to]) continue;
      extended = true;
      // The jumping piece has left its square; captured pieces stay on the
      // board until the move ends (they cannot be jumped twice).
      const b = board.slice();
      b[sq] = null;
      b[to] = piece;
      jumps(b, to, piece, path.concat(to), captured.concat(mid), out);
    }
  }
  if (!extended && path.length > 1) out.push({ path, captures: captured });
}

export function moves(s) {
  if (s._moves) return s._moves;
  const caps = [];
  const steps = [];
  for (let sq = 0; sq < 64; sq++) {
    const p = s.board[sq];
    if (!p || owner(p) !== s.side) continue;
    jumps(s.board, sq, p, [sq], [], caps);
    const r = sq >> 3;
    const c = sq & 7;
    for (const [dr, dc] of dirs(p)) {
      const tr = r + dr;
      const tc = c + dc;
      if (inside(tr, tc) && !s.board[tr * 8 + tc]) steps.push({ path: [sq, tr * 8 + tc], captures: [] });
    }
  }
  s._moves = caps.length ? caps : steps;
  return s._moves;
}

export function play(s, m) {
  const legal = moves(s).find((x) => x.path.length === m.path.length && x.path.every((v, i) => v === m.path[i]));
  if (!legal) throw new Error('That move is not allowed.');
  const board = s.board.slice();
  const from = legal.path[0];
  const to = legal.path[legal.path.length - 1];
  let piece = board[from];
  const man = !isKing(piece);
  board[from] = null;
  for (const c of legal.captures) board[c] = null;
  if (man && (to >> 3) === (s.side === 0 ? 0 : 7)) piece = piece.toUpperCase();
  board[to] = piece;
  const quiet = legal.captures.length || man ? 0 : s.quiet + 1;
  return { board, side: 1 - s.side, quiet, prev: s, last: legal };
}

export function result(s) {
  if (!moves(s).length) return { winner: 1 - s.side, reason: `${players[s.side]} cannot move` };
  if (s.quiet >= 80) return { winner: null, reason: '40 moves each without a capture or a man moving' };
  return null;
}

export function evaluate(s, player) {
  let score = 0;
  for (let sq = 0; sq < 64; sq++) {
    const p = s.board[sq];
    if (!p) continue;
    const r = sq >> 3;
    const c = sq & 7;
    let v = isKing(p) ? 160 : 100;
    if (!isKing(p)) {
      v += 3 * (owner(p) === 0 ? 7 - r : r); // advancement
      if ((owner(p) === 0 && r === 7) || (owner(p) === 1 && r === 0)) v += 8; // guarding the back row
    } else if (c > 1 && c < 6 && r > 1 && r < 6) v += 6; // central kings
    score += owner(p) === 0 ? v : -v;
  }
  return player === 0 ? score : -score;
}

export function order(s, list) {
  return list.slice().sort((a, b) => b.captures.length - a.captures.length);
}

export function noisy(s) {
  const list = moves(s);
  return list.length && list[0].captures.length ? list : [];
}

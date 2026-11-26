// Go on a 9×9, 13×13 or 19×19 board, for two players on one device.
//
// Points are row * size + column. Black (player 0) plays first. A stone or
// group with no empty neighbouring point is captured. A move may not leave
// its own group without liberties (suicide), and may not recreate the
// position after the player's own previous move (ko). After both players
// pass in turn, they mark the dead stones and the game is scored by area
// (stones plus surrounded empty points), with 7.5 points of komi for White.

export const players = ['Black', 'White'];
export const noComputer = true;
export const KOMI = 7.5;

export function newGame(opts = {}) {
  const size = [9, 13, 19].includes(Number(opts.size)) ? Number(opts.size) : 9;
  return { size, board: new Array(size * size).fill(null), side: 0, passes: 0, captured: [0, 0],
    phase: 'play', dead: [], prev: null, last: null };
}

export function turn(s) { return s.side; }

export function neighbours(size, p) {
  const r = Math.floor(p / size);
  const c = p % size;
  const out = [];
  if (r > 0) out.push(p - size);
  if (r < size - 1) out.push(p + size);
  if (c > 0) out.push(p - 1);
  if (c < size - 1) out.push(p + 1);
  return out;
}

// group returns the stones connected to p and whether they have a liberty.
export function group(board, size, p) {
  const colour = board[p];
  const stones = [p];
  const seen = new Set(stones);
  let liberty = false;
  for (let i = 0; i < stones.length; i++) {
    for (const n of neighbours(size, stones[i])) {
      if (board[n] === null) liberty = true;
      else if (board[n] === colour && !seen.has(n)) { seen.add(n); stones.push(n); }
    }
  }
  return { stones, liberty };
}

function place(s, p) {
  if (s.board[p] !== null) return null;
  const board = s.board.slice();
  board[p] = s.side;
  let taken = 0;
  for (const n of neighbours(s.size, p)) {
    if (board[n] !== 1 - s.side) continue;
    const g = group(board, s.size, n);
    if (!g.liberty) for (const q of g.stones) { board[q] = null; taken++; }
  }
  if (!group(board, s.size, p).liberty) return null; // suicide
  const before = s.prev ? s.prev.board : null;
  if (before && taken && board.every((v, i) => v === before[i])) return null; // ko
  return { board, taken };
}

export function moves(s) {
  if (s._moves) return s._moves;
  const out = [];
  if (s.phase === 'play') {
    for (let p = 0; p < s.board.length; p++) if (place(s, p)) out.push({ at: p });
    out.push({ pass: true });
  } else if (s.phase === 'scoring') {
    for (let p = 0; p < s.board.length; p++) if (s.board[p] !== null) out.push({ dead: p });
    out.push({ accept: true }, { resume: true });
  }
  return (s._moves = out);
}

export function play(s, m) {
  const base = { size: s.size, captured: s.captured, prev: s, last: m, dead: s.dead };
  if (s.phase === 'play') {
    if (m.pass) {
      const passes = s.passes + 1;
      return { ...base, board: s.board, side: 1 - s.side, passes, phase: passes >= 2 ? 'scoring' : 'play', dead: [] };
    }
    const r = Number.isInteger(m.at) && m.at >= 0 && m.at < s.board.length ? place(s, m.at) : null;
    if (!r) throw new Error('A stone cannot be played there.');
    const captured = s.captured.slice();
    captured[s.side] += r.taken;
    return { ...base, board: r.board, side: 1 - s.side, passes: 0, captured, phase: 'play' };
  }
  if (s.phase === 'scoring') {
    if (m.resume) return { ...base, board: s.board, side: s.side, passes: 0, phase: 'play', dead: [] };
    if (m.accept) return { ...base, board: s.board, side: s.side, passes: s.passes, phase: 'over' };
    if (Number.isInteger(m.dead) && s.board[m.dead] !== null) {
      // Marking a stone marks (or clears) its whole group.
      const g = group(s.board, s.size, m.dead).stones;
      const isDead = s.dead.includes(m.dead);
      const dead = isDead ? s.dead.filter((p) => !g.includes(p)) : [...new Set(s.dead.concat(g))];
      return { ...base, board: s.board, side: s.side, passes: s.passes, phase: 'scoring', dead };
    }
  }
  throw new Error('That is not possible now.');
}

// score counts area: each player's living stones and the empty points
// (including those of dead stones) that only that player's stones surround.
export function score(s) {
  const dead = new Set(s.dead);
  const board = s.board.map((v, i) => (dead.has(i) ? null : v));
  const area = [0, 0];
  const territory = new Array(board.length).fill(null);
  for (const v of board) if (v !== null) area[v]++;
  const seen = new Set();
  for (let p = 0; p < board.length; p++) {
    if (board[p] !== null || seen.has(p)) continue;
    const region = [p];
    seen.add(p);
    const borders = new Set();
    for (let i = 0; i < region.length; i++) {
      for (const n of neighbours(s.size, region[i])) {
        if (board[n] === null) { if (!seen.has(n)) { seen.add(n); region.push(n); } } else borders.add(board[n]);
      }
    }
    if (borders.size === 1) {
      const owner = [...borders][0];
      area[owner] += region.length;
      for (const q of region) territory[q] = owner;
    }
  }
  return { black: area[0], white: area[1] + KOMI, territory };
}

export function result(s) {
  if (s.phase !== 'over') return null;
  const { black, white } = score(s);
  return { winner: black > white ? 0 : 1, reason: `${black} to ${white} (komi ${KOMI})` };
}

export function evaluate() { return 0; }

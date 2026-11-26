// Chess: complete rules (castling, en passant, promotion, check, checkmate,
// stalemate, the fifty-move rule, threefold repetition and insufficient
// material) and an evaluation for the computer opponent.
//
// The board is a 0x88 array: square = rank * 16 + file, rank 0 is White's
// first rank, and (square & 0x88) !== 0 is off the board. Pieces are FEN
// letters: upper case White, lower case Black. States are never modified;
// play() returns a new one that links to its parent for repetition checks.

export const START = 'rnbqkbnr/pppppppp/8/8/8/8/PPPPPPPP/RNBQKBNR w KQkq - 0 1';
export const players = ['White', 'Black'];

const N_OFF = [33, 31, 18, 14, -33, -31, -18, -14];
const K_OFF = [1, -1, 16, -16, 15, 17, -15, -17];
const B_DIR = [15, 17, -15, -17];
const R_DIR = [1, -1, 16, -16];
const VALUE = { p: 100, n: 320, b: 330, r: 500, q: 900, k: 0 };
const ok = (sq) => (sq & 0x88) === 0;
const isWhite = (p) => p >= 'A' && p <= 'Z';
const colorOf = (p) => (isWhite(p) ? 'w' : 'b');
const rankOf = (sq) => sq >> 4;
const fileOf = (sq) => sq & 7;

export function squareName(sq) { return 'abcdefgh'[fileOf(sq)] + (rankOf(sq) + 1); }
export function squareOf(name) { return (Number(name[1]) - 1) * 16 + 'abcdefgh'.indexOf(name[0]); }

export function fromFEN(fen) {
  const [placement, side = 'w', castling = '-', ep = '-', half = '0', full = '1'] = fen.trim().split(/\s+/);
  const board = new Array(128).fill(null);
  const kings = { w: -1, b: -1 };
  const ranks = placement.split('/');
  if (ranks.length !== 8) throw new Error('A FEN position needs 8 ranks.');
  ranks.forEach((row, i) => {
    let f = 0;
    for (const ch of row) {
      if (/\d/.test(ch)) { f += Number(ch); continue; }
      if (!/[pnbrqkPNBRQK]/.test(ch) || f > 7) throw new Error(`Bad FEN piece ${ch}.`);
      const sq = (7 - i) * 16 + f;
      board[sq] = ch;
      if (ch === 'K') kings.w = sq;
      if (ch === 'k') kings.b = sq;
      f++;
    }
  });
  if (kings.w < 0 || kings.b < 0) throw new Error('Both kings must be on the board.');
  return { board, side, castling: castling === '-' ? '' : castling, ep: ep === '-' ? -1 : squareOf(ep),
    half: Number(half) || 0, full: Number(full) || 1, kings, prev: null, last: null };
}

export function toFEN(s) {
  const rows = [];
  for (let r = 7; r >= 0; r--) {
    let row = '';
    let empty = 0;
    for (let f = 0; f < 8; f++) {
      const p = s.board[r * 16 + f];
      if (!p) { empty++; continue; }
      if (empty) { row += empty; empty = 0; }
      row += p;
    }
    rows.push(row + (empty || ''));
  }
  return `${rows.join('/')} ${s.side} ${s.castling || '-'} ${s.ep >= 0 ? squareName(s.ep) : '-'} ${s.half} ${s.full}`;
}

export function newGame() { return fromFEN(START); }
export function turn(s) { return s.side === 'w' ? 0 : 1; }

export function attacked(board, sq, by) {
  const white = by === 'w';
  const pawn = white ? 'P' : 'p';
  const pawnFrom = white ? [sq - 15, sq - 17] : [sq + 15, sq + 17];
  for (const t of pawnFrom) if (ok(t) && board[t] === pawn) return true;
  const knight = white ? 'N' : 'n';
  for (const o of N_OFF) { const t = sq + o; if (ok(t) && board[t] === knight) return true; }
  const king = white ? 'K' : 'k';
  for (const o of K_OFF) { const t = sq + o; if (ok(t) && board[t] === king) return true; }
  const bishop = white ? 'B' : 'b';
  const rook = white ? 'R' : 'r';
  const queen = white ? 'Q' : 'q';
  for (const d of B_DIR) {
    for (let t = sq + d; ok(t); t += d) {
      const p = board[t];
      if (!p) continue;
      if (p === bishop || p === queen) return true;
      break;
    }
  }
  for (const d of R_DIR) {
    for (let t = sq + d; ok(t); t += d) {
      const p = board[t];
      if (!p) continue;
      if (p === rook || p === queen) return true;
      break;
    }
  }
  return false;
}

export function inCheck(s) {
  return attacked(s.board, s.kings[s.side], s.side === 'w' ? 'b' : 'w');
}

function pseudoMoves(s) {
  const out = [];
  const { board, side } = s;
  const them = side === 'w' ? 'b' : 'w';
  const add = (from, to, flag) => {
    const piece = board[from];
    const captured = flag === 'e' ? (side === 'w' ? 'p' : 'P') : board[to];
    if (piece.toLowerCase() === 'p' && (rankOf(to) === 7 || rankOf(to) === 0)) {
      for (const promo of ['q', 'r', 'b', 'n']) out.push({ from, to, piece, captured, promo, flag, path: [from, to] });
    } else out.push({ from, to, piece, captured, promo: null, flag, path: [from, to] });
  };
  for (let sq = 0; sq < 128; sq++) {
    if (!ok(sq)) { sq += 7; continue; }
    const p = board[sq];
    if (!p || colorOf(p) !== side) continue;
    const kind = p.toLowerCase();
    if (kind === 'p') {
      const dir = side === 'w' ? 16 : -16;
      const startRank = side === 'w' ? 1 : 6;
      const one = sq + dir;
      if (ok(one) && !board[one]) {
        add(sq, one, '');
        const two = one + dir;
        if (rankOf(sq) === startRank && !board[two]) add(sq, two, 'd');
      }
      for (const t of side === 'w' ? [sq + 15, sq + 17] : [sq - 15, sq - 17]) {
        if (!ok(t)) continue;
        if (board[t] && colorOf(board[t]) === them) add(sq, t, '');
        else if (t === s.ep) add(sq, t, 'e');
      }
    } else if (kind === 'n' || kind === 'k') {
      for (const o of kind === 'n' ? N_OFF : K_OFF) {
        const t = sq + o;
        if (ok(t) && (!board[t] || colorOf(board[t]) === them)) add(sq, t, '');
      }
    } else {
      const dirs = kind === 'b' ? B_DIR : kind === 'r' ? R_DIR : B_DIR.concat(R_DIR);
      for (const d of dirs) {
        for (let t = sq + d; ok(t); t += d) {
          if (!board[t]) { add(sq, t, ''); continue; }
          if (colorOf(board[t]) === them) add(sq, t, '');
          break;
        }
      }
    }
  }
  // Castling: the king may not be in check, pass through or land on an
  // attacked square, and the squares between king and rook must be empty.
  const home = side === 'w' ? 0 : 112;
  const K = side === 'w' ? 'K' : 'k';
  const R = side === 'w' ? 'R' : 'r';
  if (board[home + 4] === K && !attacked(board, home + 4, them)) {
    if (s.castling.includes(side === 'w' ? 'K' : 'k') && board[home + 7] === R && !board[home + 5] && !board[home + 6] &&
      !attacked(board, home + 5, them) && !attacked(board, home + 6, them)) {
      out.push({ from: home + 4, to: home + 6, piece: K, captured: null, promo: null, flag: 'k', path: [home + 4, home + 6] });
    }
    if (s.castling.includes(side === 'w' ? 'Q' : 'q') && board[home] === R && !board[home + 1] && !board[home + 2] && !board[home + 3] &&
      !attacked(board, home + 3, them) && !attacked(board, home + 2, them)) {
      out.push({ from: home + 4, to: home + 2, piece: K, captured: null, promo: null, flag: 'q', path: [home + 4, home + 2] });
    }
  }
  return out;
}

const CORNER_RIGHT = { 0: 'Q', 7: 'K', 112: 'q', 119: 'k' };

function apply(s, m) {
  const board = s.board.slice();
  const white = s.side === 'w';
  board[m.from] = null;
  board[m.to] = m.promo ? (white ? m.promo.toUpperCase() : m.promo) : m.piece;
  if (m.flag === 'e') board[m.to + (white ? -16 : 16)] = null;
  if (m.flag === 'k') { board[m.from + 1] = board[m.from + 3]; board[m.from + 3] = null; }
  if (m.flag === 'q') { board[m.from - 1] = board[m.from - 4]; board[m.from - 4] = null; }
  let castling = s.castling;
  if (castling) {
    if (m.piece === 'K') castling = castling.replace('K', '').replace('Q', '');
    if (m.piece === 'k') castling = castling.replace('k', '').replace('q', '');
    for (const sq of [m.from, m.to]) if (CORNER_RIGHT[sq]) castling = castling.replace(CORNER_RIGHT[sq], '');
  }
  const kings = { ...s.kings };
  if (m.piece === 'K') kings.w = m.to;
  if (m.piece === 'k') kings.b = m.to;
  const pawn = m.piece === 'P' || m.piece === 'p';
  return {
    board, side: white ? 'b' : 'w', castling,
    ep: m.flag === 'd' ? (m.from + m.to) / 2 : -1,
    half: pawn || m.captured ? 0 : s.half + 1,
    full: white ? s.full : s.full + 1,
    kings, prev: s, last: m,
  };
}

export function moves(s) {
  if (s._moves) return s._moves;
  const legal = [];
  for (const m of pseudoMoves(s)) {
    const n = apply(s, m);
    if (!attacked(n.board, n.kings[s.side], n.side)) legal.push(m);
  }
  s._moves = legal;
  return legal;
}

export function play(s, m) {
  // Accept a move from another list (a saved game) by matching it.
  const legal = moves(s).find((x) => x.from === m.from && x.to === m.to && (x.promo || null) === (m.promo || null));
  if (!legal) throw new Error(`Illegal move ${squareName(m.from)}-${squareName(m.to)}.`);
  return apply(s, legal);
}

function key(s) {
  if (!s._key) {
    // En passant counts only when a capture is actually possible.
    const ep = s.ep >= 0 && moves(s).some((m) => m.flag === 'e') ? s.ep : -1;
    s._key = toFEN({ ...s, ep, half: 0, full: 0 }).split(' ').slice(0, 4).join(' ');
  }
  return s._key;
}

function insufficient(board) {
  const minors = [];
  for (let sq = 0; sq < 128; sq++) {
    if (!ok(sq)) { sq += 7; continue; }
    const p = board[sq];
    if (!p || p === 'K' || p === 'k') continue;
    const k = p.toLowerCase();
    if (k !== 'b' && k !== 'n') return false;
    minors.push({ k, color: colorOf(p), dark: (rankOf(sq) + fileOf(sq)) % 2 === 0 });
  }
  if (minors.length <= 1) return true;
  // Only bishops, all on squares of one colour: no mate is possible.
  return minors.every((m) => m.k === 'b') && minors.every((m) => m.dark === minors[0].dark);
}

export function result(s) {
  if (s._result !== undefined) return s._result;
  let r = null;
  if (!moves(s).length) {
    r = inCheck(s) ? { winner: s.side === 'w' ? 1 : 0, reason: 'checkmate' } : { winner: null, reason: 'stalemate' };
  } else if (s.half >= 100) {
    r = { winner: null, reason: 'fifty moves without a capture or pawn move' };
  } else if (insufficient(s.board)) {
    r = { winner: null, reason: 'insufficient material to checkmate' };
  } else {
    let count = 1;
    const k = key(s);
    let n = s.prev;
    for (let i = 1; n && i <= s.half; i++, n = n.prev) {
      if (i % 2 === 0 && key(n) === k && ++count >= 3) { r = { winner: null, reason: 'threefold repetition' }; break; }
    }
  }
  s._result = r;
  return r;
}

export function perft(s, depth) {
  if (depth === 0) return 1;
  const list = moves(s);
  if (depth === 1) return list.length;
  let n = 0;
  for (const m of list) n += perft(apply(s, m), depth - 1);
  return n;
}

// Standard Algebraic Notation for move m in state s ("Nbd2", "exd6", "O-O", "e8=Q#").
export function san(s, m) {
  let out;
  if (m.flag === 'k') out = 'O-O';
  else if (m.flag === 'q') out = 'O-O-O';
  else {
    const kind = m.piece.toUpperCase();
    const capture = m.captured ? 'x' : '';
    if (kind === 'P') out = (capture ? 'abcdefgh'[fileOf(m.from)] : '') + capture + squareName(m.to) + (m.promo ? `=${m.promo.toUpperCase()}` : '');
    else {
      const rivals = moves(s).filter((x) => x.piece === m.piece && x.to === m.to && x.from !== m.from);
      let dis = '';
      if (rivals.length) {
        if (!rivals.some((x) => fileOf(x.from) === fileOf(m.from))) dis = 'abcdefgh'[fileOf(m.from)];
        else if (!rivals.some((x) => rankOf(x.from) === rankOf(m.from))) dis = String(rankOf(m.from) + 1);
        else dis = squareName(m.from);
      }
      out = kind + dis + capture + squareName(m.to);
    }
  }
  const n = apply(s, m);
  if (inCheck(n)) out += moves(n).length ? '+' : '#';
  return out;
}

// ------------------------------------------------------------ evaluation
// Piece-square tables (from White's side, rank 8 first), after Tomasz
// Michniewski's "simplified evaluation function".
const PST = {
  p: [0, 0, 0, 0, 0, 0, 0, 0, 50, 50, 50, 50, 50, 50, 50, 50, 10, 10, 20, 30, 30, 20, 10, 10, 5, 5, 10, 25, 25, 10, 5, 5,
    0, 0, 0, 20, 20, 0, 0, 0, 5, -5, -10, 0, 0, -10, -5, 5, 5, 10, 10, -20, -20, 10, 10, 5, 0, 0, 0, 0, 0, 0, 0, 0],
  n: [-50, -40, -30, -30, -30, -30, -40, -50, -40, -20, 0, 0, 0, 0, -20, -40, -30, 0, 10, 15, 15, 10, 0, -30, -30, 5, 15, 20, 20, 15, 5, -30,
    -30, 0, 15, 20, 20, 15, 0, -30, -30, 5, 10, 15, 15, 10, 5, -30, -40, -20, 0, 5, 5, 0, -20, -40, -50, -40, -30, -30, -30, -30, -40, -50],
  b: [-20, -10, -10, -10, -10, -10, -10, -20, -10, 0, 0, 0, 0, 0, 0, -10, -10, 0, 5, 10, 10, 5, 0, -10, -10, 5, 5, 10, 10, 5, 5, -10,
    -10, 0, 10, 10, 10, 10, 0, -10, -10, 10, 10, 10, 10, 10, 10, -10, -10, 5, 0, 0, 0, 0, 5, -10, -20, -10, -10, -10, -10, -10, -10, -20],
  r: [0, 0, 0, 0, 0, 0, 0, 0, 5, 10, 10, 10, 10, 10, 10, 5, -5, 0, 0, 0, 0, 0, 0, -5, -5, 0, 0, 0, 0, 0, 0, -5,
    -5, 0, 0, 0, 0, 0, 0, -5, -5, 0, 0, 0, 0, 0, 0, -5, -5, 0, 0, 0, 0, 0, 0, -5, 0, 0, 0, 5, 5, 0, 0, 0],
  q: [-20, -10, -10, -5, -5, -10, -10, -20, -10, 0, 0, 0, 0, 0, 0, -10, -10, 0, 5, 5, 5, 5, 0, -10, -5, 0, 5, 5, 5, 5, 0, -5,
    0, 0, 5, 5, 5, 5, 0, -5, -10, 5, 5, 5, 5, 5, 0, -10, -10, 0, 5, 0, 0, 0, 0, -10, -20, -10, -10, -5, -5, -10, -10, -20],
  k: [-30, -40, -40, -50, -50, -40, -40, -30, -30, -40, -40, -50, -50, -40, -40, -30, -30, -40, -40, -50, -50, -40, -40, -30, -30, -40, -40, -50, -50, -40, -40, -30,
    -20, -30, -30, -40, -40, -30, -30, -20, -10, -20, -20, -20, -20, -20, -20, -10, 20, 20, 0, 0, 0, 0, 20, 20, 20, 30, 10, 0, 0, 10, 30, 20],
  kEnd: [-50, -40, -30, -20, -20, -30, -40, -50, -30, -20, -10, 0, 0, -10, -20, -30, -30, -10, 20, 30, 30, 20, -10, -30, -30, -10, 30, 40, 40, 30, -10, -30,
    -30, -10, 30, 40, 40, 30, -10, -30, -30, -10, 20, 30, 30, 20, -10, -30, -30, -30, 0, 0, 0, 0, -30, -30, -50, -30, -30, -30, -30, -30, -30, -50],
};

export function evaluate(s, player) {
  let score = 0;
  let heavy = 0;
  for (let sq = 0; sq < 128; sq++) {
    if (!ok(sq)) { sq += 7; continue; }
    const p = s.board[sq];
    if (p && p.toLowerCase() !== 'p' && p.toLowerCase() !== 'k') heavy += VALUE[p.toLowerCase()];
  }
  const endgame = heavy <= 1300;
  for (let sq = 0; sq < 128; sq++) {
    if (!ok(sq)) { sq += 7; continue; }
    const p = s.board[sq];
    if (!p) continue;
    const k = p.toLowerCase();
    const white = isWhite(p);
    const idx = (white ? 7 - rankOf(sq) : rankOf(sq)) * 8 + fileOf(sq);
    const table = k === 'k' && endgame ? PST.kEnd : PST[k];
    const v = VALUE[k] + table[idx];
    score += white ? v : -v;
  }
  return player === 0 ? score : -score;
}

// Captures and promotions first, the most valuable victim first.
export function order(s, list) {
  const rank = (m) => (m.captured ? 10 * VALUE[m.captured.toLowerCase()] - VALUE[m.piece.toLowerCase()] + 1000 : 0) + (m.promo ? 800 : 0);
  return list.slice().sort((a, b) => rank(b) - rank(a));
}

export function noisy(s) { return moves(s).filter((m) => m.captured || m.promo); }

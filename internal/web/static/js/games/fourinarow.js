// Four in a row: players drop discs into a grid of 7 columns and 6 rows.
// The first to line up four of their own discs across, up and down or
// diagonally wins; a full grid is a draw. Cells are row * 7 + column, row 0
// at the top. Player 0 (Red) moves first.

export const players = ['Red', 'Yellow'];
export const COLS = 7;
export const ROWS = 6;

export function newGame() {
  return { board: new Array(COLS * ROWS).fill(null), side: 0, prev: null, last: null };
}

export function turn(s) { return s.side; }

function lowest(board, col) {
  for (let r = ROWS - 1; r >= 0; r--) if (board[r * COLS + col] === null) return r * COLS + col;
  return -1;
}

export function moves(s) {
  if (s._moves) return s._moves;
  if (winLine(s.board)) return (s._moves = []);
  const out = [];
  for (const col of [3, 2, 4, 1, 5, 0, 6]) {
    const cell = lowest(s.board, col);
    if (cell >= 0) out.push({ at: col, cell });
  }
  return (s._moves = out);
}

export function play(s, m) {
  const legal = moves(s).find((x) => x.at === m.at);
  if (!legal) throw new Error('That column is full.');
  const board = s.board.slice();
  board[legal.cell] = s.side;
  return { board, side: 1 - s.side, prev: s, last: legal };
}

const LINES = (() => {
  const out = [];
  for (let r = 0; r < ROWS; r++) {
    for (let c = 0; c < COLS; c++) {
      for (const [dr, dc] of [[0, 1], [1, 0], [1, 1], [1, -1]]) {
        const er = r + 3 * dr;
        const ec = c + 3 * dc;
        if (er < 0 || er >= ROWS || ec < 0 || ec >= COLS) continue;
        out.push([0, 1, 2, 3].map((k) => (r + k * dr) * COLS + c + k * dc));
      }
    }
  }
  return out;
})();

// winLine returns the four cells of a completed line, or null.
export function winLine(board) {
  for (const l of LINES) {
    const v = board[l[0]];
    if (v !== null && board[l[1]] === v && board[l[2]] === v && board[l[3]] === v) return l;
  }
  return null;
}

export function result(s) {
  const l = winLine(s.board);
  if (l) return { winner: s.board[l[0]], reason: 'four in a row', line: l };
  if (s.board.every((v) => v !== null)) return { winner: null, reason: 'the grid is full' };
  return null;
}

export function evaluate(s, player) {
  let score = 0;
  for (const l of LINES) {
    let mine = 0;
    let theirs = 0;
    for (const i of l) {
      if (s.board[i] === player) mine++;
      else if (s.board[i] === 1 - player) theirs++;
    }
    if (theirs === 0) score += [0, 1, 10, 50, 0][mine];
    if (mine === 0) score -= [0, 1, 10, 50, 0][theirs];
  }
  for (let r = 0; r < ROWS; r++) {
    const v = s.board[r * COLS + 3];
    if (v === player) score += 4; else if (v === 1 - player) score -= 4;
  }
  return score;
}

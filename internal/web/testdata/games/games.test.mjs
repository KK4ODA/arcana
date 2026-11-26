import { test } from 'node:test';
import assert from 'node:assert/strict';
import * as checkers from './checkers.js';
import * as reversi from './reversi.js';
import * as four from './fourinarow.js';
import * as mancala from './mancala.js';
import * as bg from './backgammon.js';
import * as go from './go.js';
import { GAMES } from './index.js';
import { search } from './engine.js';

const at = (r, c) => r * 8 + c;

// ------------------------------------------------------------ checkers
function checkersBoard(pieces, side = 0) {
  const board = new Array(64).fill(null);
  for (const [r, c, p] of pieces) board[at(r, c)] = p;
  return { board, side, quiet: 0, prev: null, last: null };
}

test('checkers: seven opening moves for Dark', () => {
  assert.equal(checkers.moves(checkers.newGame()).length, 7);
});

test('checkers: a capture is compulsory and must continue', () => {
  // Dark man at 5,0; Light men at 4,1 and 2,3: a double jump to 1,4.
  const s = checkersBoard([[5, 0, 'd'], [4, 1, 'l'], [2, 3, 'l'], [5, 6, 'd'], [0, 7, 'l']]);
  const list = checkers.moves(s);
  assert.equal(list.length, 1);
  assert.deepEqual(list[0].path, [at(5, 0), at(3, 2), at(1, 4)]);
  const n = checkers.play(s, list[0]);
  assert.equal(n.board[at(4, 1)], null);
  assert.equal(n.board[at(2, 3)], null);
  assert.equal(n.board[at(1, 4)], 'd');
});

test('checkers: a man crowned while jumping stops there', () => {
  // Jumping 2,1 lands on row 0 and is crowned; it may not continue as a king.
  const s = checkersBoard([[2, 0, 'd'], [1, 1, 'l'], [1, 3, 'l'], [7, 0, 'l']]);
  const m = checkers.moves(s);
  assert.deepEqual(m.map((x) => x.path), [[at(2, 0), at(0, 2)]]);
  assert.equal(checkers.play(s, m[0]).board[at(0, 2)], 'D');
});

test('checkers: a player who cannot move loses', () => {
  const s = checkersBoard([[0, 1, 'd']], 1);
  assert.equal(checkers.result(s).winner, 0);
});

// ------------------------------------------------------------ reversi
test('reversi: four opening moves, and discs turn over', () => {
  const s = reversi.newGame();
  assert.equal(reversi.moves(s).length, 4);
  const m = reversi.moves(s)[0];
  const n = reversi.play(s, m);
  assert.deepEqual(reversi.count(n), [4, 1]);
});

test('reversi: a player without a move passes, and the game ends when neither can move', () => {
  // White has no move; Black does.
  const board = new Array(64).fill(null);
  board[0] = 0; board[1] = 1; // Black could play at 2; White has nothing to turn
  let s = { board, side: 1, passes: 0, prev: null, last: null };
  assert.deepEqual(reversi.moves(s), [{ pass: true }]);
  s = reversi.play(s, { pass: true });
  assert.equal(s.side, 0);
  assert.throws(() => reversi.play({ ...reversi.newGame() }, { pass: true }));
  const full = { board: new Array(64).fill(0), side: 1, passes: 0, prev: null, last: null };
  assert.deepEqual(reversi.result(full), { winner: 0, reason: '64–0' });
});

// ------------------------------------------------------------ four in a row
test('four in a row: wins across, up and diagonally; full columns refuse discs', () => {
  const drop = (cols) => cols.reduce((s, c) => four.play(s, { at: c }), four.newGame());
  assert.equal(four.result(drop([0, 0, 1, 1, 2, 2, 3])).winner, 0);
  assert.equal(four.result(drop([0, 1, 0, 1, 0, 1, 0])).winner, 0);
  assert.equal(four.result(drop([0, 1, 1, 2, 2, 3, 2, 3, 3, 6, 3])).winner, 0);
  assert.throws(() => drop([0, 0, 0, 0, 0, 0, 0]));
});

test('four in a row: the computer takes a win and blocks a loss', () => {
  const drop = (cols) => cols.reduce((s, c) => four.play(s, { at: c }), four.newGame());
  assert.equal(search(four, drop([0, 6, 1, 6, 2]), { timeMs: 1000, maxDepth: 4 }).move.at, 3, 'blocks');
  assert.equal(search(four, drop([0, 6, 1, 6, 2, 5]), { timeMs: 1000, maxDepth: 4 }).move.at, 3, 'wins');
});

// ------------------------------------------------------------ mancala
test('mancala: ending in the store gives another turn', () => {
  const s = mancala.newGame();
  const n = mancala.play(s, { at: 2 }); // four seeds from pit 2 reach the store
  assert.equal(n.pits[6], 1);
  assert.equal(n.side, 0);
  assert.ok(n.last.again);
});

test('mancala: sowing skips the opponent’s store and captures from an empty pit', () => {
  const pits = new Array(14).fill(0);
  pits[0] = 1; pits[1] = 0; pits[11] = 5; pits[12] = 1;
  const s = { pits, side: 0, over: false, prev: null, last: null };
  const n = mancala.play(s, { at: 0 });
  assert.equal(n.pits[6], 6, 'one seed and the five opposite');
  assert.equal(n.pits[11], 0);
  const lap = new Array(14).fill(0);
  lap[5] = 14; lap[7] = 1;
  const m = mancala.play({ pits: lap, side: 0, over: false, prev: null, last: null }, { at: 5 });
  assert.equal(m.pits[13], 0, 'North’s store is skipped');
});

test('mancala: the game ends when a side is empty', () => {
  const pits = new Array(14).fill(0);
  pits[5] = 1; pits[8] = 3; pits[6] = 20; pits[13] = 24;
  const n = mancala.play({ pits, side: 0, over: false, prev: null, last: null }, { at: 5 });
  assert.ok(n.over);
  assert.equal(n.pits[13], 27);
  assert.deepEqual(mancala.result(n), { winner: 1, reason: '21–27' });
});

// ------------------------------------------------------------ backgammon
function bgState(setup, extra = {}) {
  const pts = new Array(25).fill(0);
  for (const [p, n] of Object.entries(setup)) pts[p] = n;
  return { pts, bar: [0, 0], off: [0, 0], side: 0, phase: 'roll', dice: [], rolled: null, prev: null, last: null, note: '', ...extra };
}

test('backgammon: the opening roll decides who starts', () => {
  const s = bg.newGame();
  const tie = bg.roll(s, [3, 3]);
  assert.equal(tie.phase, 'opening');
  const n = bg.roll(tie, [2, 5]);
  assert.equal(n.side, 1);
  assert.deepEqual(n.dice, [2, 5]);
  assert.ok(bg.moves(n).length > 0);
});

test('backgammon: doubles move four times and a blot is hit', () => {
  let s = bg.roll(bgState({ 13: 2, 6: 2, 9: -1, 1: -2 }), [2, 2]);
  assert.equal(s.dice.length, 4);
  s = bg.play(s, { path: [13, 11] });
  s = bg.play(s, { path: [11, 9] });
  assert.equal(s.bar[1], 1, 'Black’s blot is on the bar');
  assert.equal(s.pts[9], 1);
  s = bg.play(s, { path: [6, 4] });
  s = bg.play(s, { path: [6, 4] });
  assert.equal(s.side, 1);
  assert.equal(s.phase, 'roll');
});

test('backgammon: a checker on the bar must enter first, and closed points block it', () => {
  const s = bg.roll(bgState({ 6: 2, 8: 2, 20: -2, 22: -2 }, { bar: [1, 0] }), [3, 5]);
  // Entry points: 25-3 = 22 (closed), 25-5 = 20 (closed): no move at all.
  assert.equal(s.side, 1);
  assert.equal(s.note, 'blocked');
  const t = bg.roll(bgState({ 6: 2, 8: 2, 20: -2 }, { bar: [1, 0] }), [3, 5]);
  assert.deepEqual(bg.moves(t).map((m) => m.path), [['bar', 22]]);
});

test('backgammon: both dice must be used, or the higher one', () => {
  // With 6-1, moving 7→6 leaves the 6 unplayable; 24→23 then 23→17 uses both.
  const s = bg.roll(bgState({ 24: 1, 7: 1, 18: -2, 1: -2, 12: -11 }), [6, 1]);
  assert.deepEqual(bg.moves(s).map((m) => m.path), [[24, 23]]);
  for (const seq of bg.sequences(s)) assert.equal(seq.length, 2);
  // Either die alone can be played from 10, but not both: it must be the 6.
  const t = bg.roll(bgState({ 10: 1, 1: -2, 24: -13 }), [6, 3]);
  assert.deepEqual(bg.moves(t).map((m) => `${m.path}|${m.die}`), ['10,4|6']);
});

test('backgammon: bearing off with exact and higher dice', () => {
  const s = bg.roll(bgState({ 4: 1, 2: 1, 24: -15 }, { off: [13, 0] }), [6, 1]);
  const paths = bg.moves(s).map((m) => `${m.path}|${m.die}`).sort();
  assert.deepEqual(paths, ['2,1|1', '4,3|1', '4,off|6']);
  let n = bg.play(s, { path: [4, 'off'] });
  n = bg.play(n, { path: [2, 1] });
  assert.equal(n.phase, 'roll');
  const won = bg.play(bg.play(bg.roll(bgState({ 1: 2, 24: -15 }, { off: [13, 0] }), [1, 2]), { path: [1, 'off'], die: 1 }), { path: [1, 'off'] });
  assert.equal(won.phase, 'over');
  assert.deepEqual(bg.result(won), { winner: 0, reason: 'gammon', points: 2 });
});

test('backgammon: the computer plays a whole legal turn', () => {
  let s = bg.roll(bg.newGame(), [6, 1]);
  const seq = bg.bestTurn(s);
  assert.equal(seq.length, 2);
  for (const step of seq) s = bg.play(s, step);
  assert.equal(s.side, 1);
  assert.equal(bg.pips(s, 0), 167 - 7);
});

// ------------------------------------------------------------ go
test('go: capture, suicide and ko', () => {
  let s = go.newGame({ size: 9 });
  const p = (r, c) => r * 9 + c;
  const seq = [p(0, 1), p(0, 0), p(1, 0), p(8, 8)]; // Black surrounds the corner stone
  for (const at of seq.slice(0, 3)) s = go.play(s, { at });
  assert.equal(s.board[p(0, 0)], null, 'White’s corner stone is captured');
  assert.equal(s.captured[0], 1);
  assert.throws(() => go.play(s, { at: p(0, 0) }), /cannot/, 'suicide is refused');
  // A ko shape: Black takes, White may not retake at once.
  let k = go.newGame({ size: 9 });
  for (const at of [p(1, 2), p(1, 3), p(2, 1), p(2, 4), p(3, 2), p(3, 3), p(8, 8), p(2, 2), p(2, 3)]) k = go.play(k, { at });
  assert.equal(k.board[p(2, 2)], null, 'Black captured at 2,2 by playing 2,3');
  assert.throws(() => go.play(k, { at: p(2, 2) }), /cannot/, 'the immediate retake is ko');
});

test('go: two passes start scoring; dead stones count for the other side', () => {
  let s = go.newGame({ size: 9 });
  const p = (r, c) => r * 9 + c;
  for (let r = 0; r < 9; r++) { s = go.play(s, { at: p(r, 4) }); s = go.play(s, { at: p(r, 5) }); }
  s = go.play(s, { at: p(0, 0) });
  s = go.play(s, { at: p(4, 1) }); // a lone White stone inside Black's area
  s = go.play(s, { pass: true });
  s = go.play(s, { pass: true });
  assert.equal(s.phase, 'scoring');
  s = go.play(s, { dead: p(4, 1) });
  s = go.play(s, { accept: true });
  const sc = go.score(s);
  assert.equal(sc.black, 45);
  assert.equal(sc.white, 36 + go.KOMI);
  assert.equal(go.result(s).winner, 0);
});

// ------------------------------------------------------------ all games
test('every game plays to the end against itself', () => {
  for (const g of GAMES) {
    if (g.rules.noComputer || g.rules.chance) continue;
    let s = g.rules.newGame();
    let n = 0;
    while (!g.rules.result(s) && n++ < 400) {
      const { move } = search(g.rules, s, { timeMs: 5, maxDepth: 1, noise: 1000, random: mulberry(n) });
      s = g.rules.play(s, move);
    }
    assert.ok(g.rules.result(s) || n >= 400, g.id);
  }
});

test('backgammon plays to the end against itself', () => {
  let s = bg.newGame();
  const rnd = mulberry(7);
  const die = () => 1 + Math.floor(rnd() * 6);
  for (let i = 0; i < 2000 && s.phase !== 'over'; i++) {
    if (s.phase === 'opening' || s.phase === 'roll') { s = bg.roll(s, [die(), die()]); continue; }
    for (const step of bg.bestTurn(s)) s = bg.play(s, step);
  }
  assert.equal(s.phase, 'over');
  assert.ok(bg.result(s).winner === 0 || bg.result(s).winner === 1);
  assert.equal(s.off[bg.result(s).winner], 15);
});

function mulberry(a) {
  return () => {
    a |= 0; a = (a + 0x6d2b79f5) | 0;
    let t = Math.imul(a ^ (a >>> 15), 1 | a);
    t = (t + Math.imul(t ^ (t >>> 7), 61 | t)) ^ t;
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

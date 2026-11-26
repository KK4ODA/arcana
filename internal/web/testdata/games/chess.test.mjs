import { test } from 'node:test';
import assert from 'node:assert/strict';
import * as chess from './chess.js';
import { search } from './engine.js';

const sq = chess.squareOf;
const mv = (s, from, to, promo) => chess.play(s, { from: sq(from), to: sq(to), promo });
const line = (...pairs) => pairs.reduce((s, p) => mv(s, p.slice(0, 2), p.slice(2, 4), p[4]), chess.newGame());

// Move-generation counts (perft) from the Chess Programming Wiki's test
// positions; a single wrong rule changes them.
const PERFT = [
  ['rnbqkbnr/pppppppp/8/8/8/8/PPPPPPPP/RNBQKBNR w KQkq - 0 1', [20, 400, 8902]],
  ['r3k2r/p1ppqpb1/bn2pnp1/3PN3/1p2P3/2N2Q1p/PPPBBPPP/R3K2R w KQkq - 0 1', [48, 2039, 97862]],
  ['8/2p5/3p4/KP5r/1R3p1k/8/4P1P1/8 w - - 0 1', [14, 191, 2812, 43238]],
  ['r3k2r/Pppp1ppp/1b3nbN/nP6/BBP1P3/q4N2/Pp1P2PP/R2Q1RK1 w kq - 0 1', [6, 264, 9467]],
  ['rnbq1k1r/pp1Pbppp/2p5/8/2B5/8/PPP1NnPP/RNBQK2R w KQ - 1 8', [44, 1486, 62379]],
];

for (const [fen, counts] of PERFT) {
  test(`perft ${fen}`, () => {
    const s = chess.fromFEN(fen);
    counts.forEach((n, i) => assert.equal(chess.perft(s, i + 1), n, `depth ${i + 1}`));
  });
}

test('FEN round trip', () => {
  for (const [fen] of PERFT) assert.equal(chess.toFEN(chess.fromFEN(fen)), fen);
});

test('fool’s mate is checkmate for Black', () => {
  const s = line('f2f3', 'e7e5', 'g2g4', 'd8h4');
  assert.deepEqual(chess.result(s), { winner: 1, reason: 'checkmate' });
  assert.equal(chess.san(s.prev, s.last), 'Qh4#');
});

test('stalemate is a draw', () => {
  const s = chess.fromFEN('7k/5Q2/6K1/8/8/8/8/8 b - - 0 1');
  assert.equal(chess.result(s).reason, 'stalemate');
  assert.equal(chess.result(s).winner, null);
});

test('en passant captures the pawn that passed', () => {
  const s = line('e2e4', 'a7a6', 'e4e5', 'd7d5');
  const ep = chess.moves(s).find((m) => m.flag === 'e');
  assert.ok(ep, 'en passant is offered');
  const n = chess.play(s, ep);
  assert.equal(n.board[sq('d5')], null);
  assert.equal(n.board[sq('d6')], 'P');
  assert.equal(chess.san(s, ep), 'exd6');
});

test('castling moves the rook, and is refused through check', () => {
  const s = chess.fromFEN('r3k2r/8/8/8/8/8/8/R3K2R w KQkq - 0 1');
  const n = mv(s, 'e1', 'g1');
  assert.equal(n.board[sq('f1')], 'R');
  assert.equal(n.board[sq('h1')], null);
  assert.equal(n.castling, 'kq');
  const attackedF1 = chess.fromFEN('4k3/8/8/8/8/8/5r2/R3K2R w KQ - 0 1');
  assert.ok(!chess.moves(attackedF1).some((m) => m.flag === 'k'), 'cannot castle through f1');
  assert.ok(chess.moves(attackedF1).some((m) => m.flag === 'q'), 'queen side is still open');
  const inCheck = chess.fromFEN('4k3/8/8/8/8/8/4r3/R3K2R w KQ - 0 1');
  assert.ok(!chess.moves(inCheck).some((m) => m.flag === 'k' || m.flag === 'q'), 'cannot castle out of check');
});

test('promotion offers four pieces and writes them', () => {
  const s = chess.fromFEN('8/P6k/8/8/8/8/8/K7 w - - 0 1');
  const promos = chess.moves(s).filter((m) => m.from === sq('a7')).map((m) => m.promo).sort();
  assert.deepEqual(promos, ['b', 'n', 'q', 'r']);
  const n = mv(s, 'a7', 'a8', 'n');
  assert.equal(n.board[sq('a8')], 'N');
});

test('threefold repetition is a draw', () => {
  const s = line('g1f3', 'g8f6', 'f3g1', 'f6g8', 'g1f3', 'g8f6', 'f3g1', 'f6g8');
  assert.equal(chess.result(s).reason, 'threefold repetition');
  assert.equal(chess.result(s.prev), null);
});

test('insufficient material and the fifty-move rule', () => {
  assert.equal(chess.result(chess.fromFEN('8/8/4k3/8/8/2B5/4K3/8 w - - 0 1')).reason, 'insufficient material to checkmate');
  assert.equal(chess.result(chess.fromFEN('8/8/4k3/8/8/2R5/4K3/8 w - - 0 1')), null);
  assert.match(chess.result(chess.fromFEN('8/8/4k3/8/8/2R5/4K3/8 w - - 100 80')).reason, /fifty/);
});

test('SAN disambiguates', () => {
  const s = chess.fromFEN('4k3/8/8/8/8/8/8/R4RK1 w - - 0 1');
  const m = chess.moves(s).find((x) => x.from === sq('a1') && x.to === sq('d1'));
  assert.equal(chess.san(s, m), 'Rad1');
  const n = chess.moves(s).find((x) => x.from === sq('f1') && x.to === sq('f8'));
  assert.equal(chess.san(s, n), 'Rf8+');
});

test('the computer finds mate in one and does not drop its queen', () => {
  const mate = chess.fromFEN('6k1/5ppp/8/8/8/8/5PPP/3R2K1 w - - 0 1');
  const { move } = search(chess, mate, { timeMs: 2000, maxDepth: 3 });
  assert.equal(chess.squareName(move.to), 'd8');
  // d5 is defended by the pawn on e6: Qxd5 exd5 loses the queen.
  const guarded = chess.fromFEN('4k3/8/4p3/3p4/8/8/3Q4/4K3 w - - 0 1');
  for (const maxDepth of [1, 3]) {
    assert.notEqual(chess.squareName(search(chess, guarded, { timeMs: 2000, maxDepth }).move.to), 'd5', `depth ${maxDepth}`);
  }
  // Undefended, the pawn is won (deeper searches may first give check).
  const free = chess.fromFEN('4k3/8/8/3p4/8/8/3Q4/4K3 w - - 0 1');
  assert.equal(chess.squareName(search(chess, free, { timeMs: 2000, maxDepth: 1 }).move.to), 'd5');
  assert.ok(search(chess, free, { timeMs: 2000, maxDepth: 3 }).score >= 90);
});

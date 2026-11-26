// Game-tree search for the computer opponent in two-player board games.
//
// A game module provides pure functions over plain, immutable states:
//   turn(s)          → 0 or 1, the player to move
//   moves(s)         → the legal moves (never empty unless the game is over)
//   play(s, m)       → the state after move m
//   result(s)        → null while the game goes on, else { winner: 0 | 1 | null }
//   evaluate(s, p)   → a heuristic score for player p (larger is better)
// and optionally:
//   order(s, moves)  → the moves sorted best-first, for faster pruning
//   noisy(s)         → the moves to keep searching at the horizon (captures),
//                      so a capture is never the last move looked at
//
// The search is negamax with alpha-beta pruning and iterative deepening
// under a time budget. A player may move twice in a row (Kalah's extra
// turn); the score is then not negated. No module imports anything, so the
// same files run in the browser and in Node's test runner.

export const WIN = 1e6;

export function search(game, state, opts = {}) {
  const timeMs = opts.timeMs ?? 500;
  const maxDepth = opts.maxDepth ?? 64;
  const noise = opts.noise ?? 0;
  const random = opts.random ?? Math.random;
  const now = opts.now ?? (() => (typeof performance !== 'undefined' ? performance.now() : Date.now()));
  const deadline = now() + timeMs;
  const root = game.turn(state);
  let rootMoves = game.moves(state);
  if (!rootMoves.length) return { move: null, score: 0, depth: 0 };
  if (rootMoves.length === 1) return { move: rootMoves[0], score: 0, depth: 0 };
  if (game.order) rootMoves = game.order(state, rootMoves);

  let nodes = 0;
  let stopped = false;
  const out = () => {
    if ((++nodes & 1023) === 0 && now() > deadline) stopped = true;
    return stopped;
  };

  function terminal(s, ply) {
    const r = game.result(s);
    if (!r) return null;
    if (r.winner === null || r.winner === undefined) return 0;
    return r.winner === game.turn(s) ? WIN - ply : -(WIN - ply);
  }

  function quiesce(s, alpha, beta, ply, qdepth) {
    const t = terminal(s, ply);
    if (t !== null) return t;
    const me = game.turn(s);
    const stand = game.evaluate(s, me);
    if (qdepth <= 0 || stand >= beta) return stand;
    if (stand > alpha) alpha = stand;
    let list = game.noisy(s);
    if (game.order) list = game.order(s, list);
    for (const m of list) {
      if (out()) break;
      const c = game.play(s, m);
      const same = game.turn(c) === me;
      const v = same ? quiesce(c, alpha, beta, ply + 1, qdepth - 1) : -quiesce(c, -beta, -alpha, ply + 1, qdepth - 1);
      if (v >= beta) return v;
      if (v > alpha) alpha = v;
    }
    return alpha;
  }

  function negamax(s, depth, alpha, beta, ply) {
    const t = terminal(s, ply);
    if (t !== null) return t;
    if (depth <= 0) return game.noisy ? quiesce(s, alpha, beta, ply, 6) : game.evaluate(s, game.turn(s));
    const me = game.turn(s);
    let list = game.moves(s);
    if (game.order) list = game.order(s, list);
    let best = -Infinity;
    for (const m of list) {
      if (out()) break;
      const c = game.play(s, m);
      const same = game.turn(c) === me;
      const v = same ? negamax(c, depth - 1, alpha, beta, ply + 1) : -negamax(c, depth - 1, -beta, -alpha, ply + 1);
      if (v > best) best = v;
      if (v > alpha) alpha = v;
      if (alpha >= beta) break;
    }
    return best;
  }

  // Each depth is searched completely before it counts; a depth cut short
  // by the clock is used only if it already found a better move.
  let best = { move: rootMoves[0], score: -Infinity, depth: 0 };
  for (let depth = 1; depth <= maxDepth; depth++) {
    let alpha = -Infinity;
    let depthBest = null;
    const scored = [];
    for (const m of rootMoves) {
      const c = game.play(state, m);
      const same = game.turn(c) === root;
      let v = same ? negamax(c, depth - 1, alpha, Infinity, 1) : -negamax(c, depth - 1, -Infinity, -alpha, 1);
      if (stopped && depth > 1) break;
      if (noise) v += (random() - 0.5) * noise;
      scored.push({ m, v });
      if (!depthBest || v > depthBest.score) depthBest = { move: m, score: v, depth };
      if (v > alpha) alpha = v;
    }
    if (depthBest && (!stopped || depth === 1 || depthBest.score > best.score)) best = depthBest;
    if (stopped || Math.abs(best.score) >= WIN - 1000) break;
    // Search the best move first at the next depth.
    scored.sort((a, b) => b.v - a.v);
    rootMoves = scored.map((x) => x.m).concat(rootMoves.filter((m) => !scored.some((x) => x.m === m)));
    if (now() > deadline) break;
  }
  return best;
}

// Levels of the computer opponent: how long it thinks and how much it
// blurs its judgement.
export const LEVELS = {
  easy: { timeMs: 150, maxDepth: 2, noise: 250 },
  medium: { timeMs: 500, maxDepth: 4, noise: 30 },
  hard: { timeMs: 1500, maxDepth: 64, noise: 0 },
};

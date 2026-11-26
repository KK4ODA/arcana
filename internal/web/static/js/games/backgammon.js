// Backgammon, without the doubling cube.
//
// Points are numbered 1 to 24 from player 0's (White's) side: White moves
// from 24 towards 1, its home board is 1–6, and its checkers on the bar
// enter on 25 minus the die. Black moves the other way, home 19–24. In
// pts[], a positive count is White's checkers and a negative one Black's.
//
// Each turn starts with a roll. Every die must be used if possible; when
// only one of two dice can be used, it must be the higher one if that can
// be played. Doubles are played four times. A point with two or more of
// the opponent's checkers is closed; a single one (a blot) is hit and sent
// to the bar. Checkers on the bar must enter before any other moves.
// Bearing off starts when all fifteen are in the home board.

export const players = ['White', 'Black'];
export const chance = true;

export function newGame() {
  const pts = new Array(25).fill(0);
  pts[24] = 2; pts[13] = 5; pts[8] = 3; pts[6] = 5;
  pts[1] = -2; pts[12] = -5; pts[17] = -3; pts[19] = -5;
  return { pts, bar: [0, 0], off: [0, 0], side: 0, phase: 'opening', dice: [], rolled: null, prev: null, last: null, note: '' };
}

export function turn(s) { return s.side; }

// Player-relative points: 1–6 is always the player's home board.
const rel = (p, point) => (p === 0 ? point : 25 - point);
const own = (s, p, q) => (p === 0 ? s.pts[q] : -s.pts[25 - q]); // >0 mine, <0 theirs at relative q

function allHome(s, p) {
  if (s.bar[p]) return false;
  for (let q = 7; q <= 24; q++) if (own(s, p, q) > 0) return false;
  return true;
}

function stepsForDie(s, p, d) {
  const out = [];
  if (s.bar[p] > 0) {
    const q = 25 - d;
    if (own(s, p, q) >= -1) out.push({ path: ['bar', rel(p, q)], die: d });
    return out;
  }
  const home = allHome(s, p);
  let highest = 0;
  for (let q = 24; q >= 1; q--) if (own(s, p, q) > 0) { highest = q; break; }
  for (let q = 1; q <= 24; q++) {
    if (own(s, p, q) <= 0) continue;
    const t = q - d;
    if (t >= 1) {
      if (own(s, p, t) >= -1) out.push({ path: [rel(p, q), rel(p, t)], die: d });
    } else if (home && (t === 0 || q === highest)) {
      out.push({ path: [rel(p, q), 'off'], die: d });
    }
  }
  return out;
}

function applyStep(s, step) {
  const p = s.side;
  const sign = p === 0 ? 1 : -1;
  const pts = s.pts.slice();
  const bar = s.bar.slice();
  const off = s.off.slice();
  const [from, to] = step.path;
  let hit = false;
  if (from === 'bar') bar[p]--; else pts[from] -= sign;
  if (to === 'off') off[p]++;
  else {
    if (pts[to] === -sign) { pts[to] = 0; bar[1 - p]++; hit = true; }
    pts[to] += sign;
  }
  const dice = s.dice.slice();
  dice.splice(dice.indexOf(step.die), 1);
  return { ...s, pts, bar, off, dice, hit };
}

// sequences lists every way to play the remaining dice that uses as many
// of them as possible (and the higher die when only one can be used).
export function sequences(s) {
  const p = s.side;
  let best = 0;
  let found = [];
  const seen = new Set();
  function walk(st, seq) {
    const values = [...new Set(st.dice)];
    let extended = false;
    for (const d of values) {
      for (const step of stepsForDie(st, p, d)) {
        extended = true;
        walk(applyStep(st, step), seq.concat(step));
      }
    }
    if (!extended && seq.length) {
      if (seq.length > best) { best = seq.length; found = []; seen.clear(); }
      if (seq.length === best) {
        const k = `${st.pts.join(',')}|${st.bar}|${st.off}|${seq[0].path}|${seq[0].die}`;
        if (!seen.has(k)) { seen.add(k); found.push(seq); }
      }
    }
  }
  walk(s, []);
  if (best === 1 && s.dice.length === 2 && s.dice[0] !== s.dice[1]) {
    const high = Math.max(...s.dice);
    if (found.some((q) => q[0].die === high)) found = found.filter((q) => q[0].die === high);
  }
  return found;
}

export function moves(s) {
  if (s._moves) return s._moves;
  if (s.phase !== 'move') return (s._moves = []);
  const out = [];
  const seen = new Set();
  // Smaller dice first, so that bearing off uses the smaller die that
  // reaches when either would.
  const all = sequences(s).map((q) => q[0]).sort((a, b) => a.die - b.die);
  for (const step of all) {
    const k = `${step.path}|${step.die}`;
    if (!seen.has(k)) { seen.add(k); out.push(step); }
  }
  return (s._moves = out);
}

function endTurn(s, note) {
  if (s.off[s.side] === 15) return { ...s, phase: 'over', dice: [], note };
  return { ...s, side: 1 - s.side, phase: 'roll', dice: [], note };
}

export function play(s, m) {
  const legal = moves(s).find((x) => x.path[0] === m.path[0] && x.path[1] === m.path[1] && (m.die === undefined || x.die === m.die));
  if (!legal) throw new Error('That checker cannot move there with these dice.');
  const { _moves, ...after } = applyStep(s, legal);
  const n = { ...after, prev: s, last: legal, note: '' };
  if (!n.dice.length || n.off[n.side] === 15 || !moves(n).length) {
    const { _moves: stale, ...rest } = n;
    return endTurn(rest, '');
  }
  return n;
}

// roll applies a throw of the dice: at the opening, each player's single
// die (the higher starts with both numbers); later, the two dice of the
// player to move.
export function roll(s, dice) {
  const [a, b] = dice;
  if (![a, b].every((d) => Number.isInteger(d) && d >= 1 && d <= 6)) throw new Error('Dice show 1 to 6.');
  const { _moves, ...base } = s; // the cached move list belongs to s only
  if (s.phase === 'opening') {
    if (a === b) return { ...base, rolled: [a, b], note: 'tie', prev: s, last: { roll: [a, b] } };
    return { ...base, side: a > b ? 0 : 1, phase: 'move', dice: [a, b], rolled: [a, b], note: '', prev: s, last: { roll: [a, b] } };
  }
  if (s.phase !== 'roll') throw new Error('It is not time to roll.');
  const n = { ...base, phase: 'move', dice: a === b ? [a, a, a, a] : [a, b], rolled: [a, b], note: '', prev: s, last: { roll: [a, b] } };
  if (!moves(n).length) {
    const { _moves: stale, ...rest } = n;
    return endTurn(rest, 'blocked');
  }
  return n;
}

export function pips(s, p) {
  let n = s.bar[p] * 25;
  for (let q = 1; q <= 24; q++) { const c = own(s, p, q); if (c > 0) n += c * q; }
  return n;
}

export function result(s) {
  for (const p of [0, 1]) {
    if (s.off[p] !== 15) continue;
    const o = 1 - p;
    let points = 1;
    let reason = 'single game';
    if (s.off[o] === 0) {
      points = 2;
      reason = 'gammon';
      let behind = s.bar[o] > 0;
      for (let q = 1; q <= 6; q++) if (own(s, p, q) < 0) behind = true;
      if (behind) { points = 3; reason = 'backgammon'; }
    }
    return { winner: p, reason, points };
  }
  return null;
}

// evaluate scores a position for player p after p has moved.
export function evaluate(s, p) {
  const o = 1 - p;
  let score = (pips(s, o) - pips(s, p)) * 2 + (s.off[p] - s.off[o]) * 4;
  // Contact: does any of the opponent's checkers still have to pass ours?
  let myBack = s.bar[p] ? 25 : 0;
  for (let q = 24; q >= 1 && !myBack; q--) if (own(s, p, q) > 0) myBack = q;
  let theirBack = s.bar[o] ? 25 : 0;
  for (let q = 24; q >= 1 && !theirBack; q--) if (own(s, o, q) > 0) theirBack = q;
  const contact = myBack + theirBack > 25;
  if (!contact) return score;
  let run = 0;
  let prime = 0;
  for (let q = 1; q <= 24; q++) {
    const c = own(s, p, q);
    if (c >= 2) {
      score += q <= 6 ? 8 : q >= 19 ? 5 : 3;
      run++;
      prime = Math.max(prime, run);
    } else run = 0;
    if (c === 1) {
      // A blot the opponent could reach: losing it costs its progress.
      const oq = 25 - q; // the same point from the opponent's side
      let exposed = s.bar[o] > 0;
      for (let k = oq + 1; k <= 24 && !exposed; k++) if (own(s, o, k) > 0) exposed = true;
      if (exposed) score -= 4 + (25 - q) * 0.6;
    }
  }
  if (prime >= 3) score += (prime - 2) * 6;
  score += s.bar[o] * 10 - s.bar[p] * 10;
  return score;
}

// bestTurn chooses the computer's whole turn: the sequence whose final
// position scores best, with some noise on the easier levels.
export function bestTurn(s, opts = {}) {
  const random = opts.random ?? Math.random;
  const noise = opts.noise ?? 0;
  let best = null;
  for (const seq of sequences(s)) {
    let st = s;
    for (const step of seq) st = applyStep(st, step);
    const v = evaluate(st, s.side) + (noise ? (random() - 0.5) * noise : 0);
    if (!best || v > best.v) best = { v, seq };
  }
  return best ? best.seq : [];
}

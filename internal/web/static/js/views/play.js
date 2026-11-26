import { h, fill, toast } from '../util.js';
import { byId } from '../games/index.js';
import { search, LEVELS } from '../games/engine.js';
import { squareName, inCheck, san } from '../games/chess.js';
import { winLine, COLS, ROWS } from '../games/fourinarow.js';
import { count as reversiCount } from '../games/reversi.js';
import { pips, bestTurn } from '../games/backgammon.js';
import { score as goScore } from '../games/go.js';

// A board game page: the board, whose turn it is, and the controls for
// playing two players on this device or against the computer. The game in
// progress is kept in this browser (localStorage) so a reload or a closed
// tab does not lose it; nothing is sent to the server, so the games work
// the same for network visitors as on the computer running Arcana.

const STORE = (id) => `arcana-game:${id}`;
const sameCells = (a, b) => a.length <= b.length && a.every((v, i) => v === b[i]);

function load(id) {
  try { return JSON.parse(localStorage.getItem(STORE(id)) || 'null'); } catch (_) { return null; }
}
function store(id, v) {
  try { localStorage.setItem(STORE(id), JSON.stringify(v)); } catch (_) { /* not kept: storage unavailable */ }
}

function rollDice() {
  const b = new Uint8Array(8);
  const out = [];
  while (out.length < 2) {
    crypto.getRandomValues(b);
    // Reject 252–255 so each face is equally likely.
    for (const x of b) if (x < 252 && out.length < 2) out.push(1 + (x % 6));
  }
  return out;
}

export async function playView(main, gameId) {
  const g = byId[gameId];
  if (!g) {
    main.appendChild(h('div', { class: 'banner err' }, 'There is no such game.'), h('a', { class: 'button', href: '#/entertainment' }, '← Entertainment'));
    return null;
  }
  const R = g.rules;
  document.title = `${g.title} — Arcana`;
  const saved = load(g.id) || {};
  const cfg = {
    mode: R.noComputer ? 'two' : saved.mode || 'computer',
    human: saved.human === 1 ? 1 : 0,
    level: LEVELS[saved.level] ? saved.level : 'medium',
    opts: saved.opts || {},
  };
  let history = []; // [{ m, by: 'human' | 'computer' | 'dice' }]
  let states = [];
  let sel = [];
  let alive = true;
  let thinking = false;
  let timer = null;

  function start(moves) {
    states = [R.newGame(cfg.opts)];
    history = [];
    for (const e of moves || []) {
      try { states.push(e.m.roll ? R.roll(cur(), e.m.roll) : R.play(cur(), e.m)); history.push(e); } catch (_) { break; }
    }
    sel = [];
  }
  const cur = () => states[states.length - 1];
  const persist = () => store(g.id, { ...cfg, moves: history });
  const computerSide = () => (cfg.mode === 'computer' ? 1 - cfg.human : -1);
  const humansTurn = () => cfg.mode === 'two' || R.turn(cur()) === cfg.human;

  function apply(m, by) {
    const s = cur();
    let n;
    try { n = m.roll ? R.roll(s, m.roll) : R.play(s, m); } catch (e) { toast(e.message); return; }
    states.push(n);
    history.push({ m: slim(m), by });
    sel = [];
    persist();
    render();
    schedule();
  }

  // Saved moves keep only what play() needs to find the move again.
  function slim(m) {
    if (m.roll) return { roll: m.roll };
    if (g.id === 'chess') return { from: m.from, to: m.to, promo: m.promo || null };
    if (m.path) return m.die !== undefined ? { path: m.path, die: m.die } : { path: m.path };
    const { flips, cell, ...rest } = m;
    return rest;
  }

  function schedule() {
    clearTimeout(timer);
    if (!alive || R.result(cur())) return;
    const s = cur();
    if (R.chance) {
      if (s.phase === 'opening') return; // a person rolls the opening
      if (R.turn(s) !== computerSide()) return;
      thinking = true;
      timer = setTimeout(() => {
        if (!alive) return;
        if (cur().phase === 'roll') { apply({ roll: rollDice() }, 'dice'); return; }
        const level = g.levels[cfg.level] || {};
        const steps = bestTurn(cur(), { noise: level.noise });
        thinking = false;
        const next = () => {
          if (!alive || !steps.length || cur().phase !== 'move' || R.turn(cur()) !== computerSide()) { render(); schedule(); return; }
          const step = steps.shift();
          states.push(R.play(cur(), step));
          history.push({ m: slim(step), by: 'computer' });
          persist();
          render();
          timer = setTimeout(next, 550);
        };
        next();
      }, 700);
      render();
      return;
    }
    if (R.turn(s) !== computerSide()) { thinking = false; return; }
    thinking = true;
    render();
    // Let the page paint "Thinking…" before the search takes the thread.
    timer = setTimeout(() => {
      if (!alive) return;
      const { move } = search(R, cur(), { ...LEVELS[cfg.level], ...(g.levels?.[cfg.level] || {}) });
      thinking = false;
      if (move) apply(move, 'computer');
    }, 60);
  }

  function undo() {
    clearTimeout(timer);
    thinking = false;
    const pop = () => { history.pop(); states.pop(); };
    const last = () => history[history.length - 1];
    if (cfg.mode === 'computer') {
      while (history.length && last().by === 'computer') pop();
      if (history.length && last().by === 'human') pop();
    } else if (history.length && last().by !== 'dice') pop();
    sel = [];
    persist();
    render();
    schedule();
  }
  const canUndo = () => history.length > 0 && history[history.length - 1].by !== 'dice' &&
    (cfg.mode === 'two' || history.some((e) => e.by === 'human'));

  // ------------------------------------------------------------ input
  function onCell(cell) {
    const s = cur();
    if (thinking || R.result(s) || !humansTurn()) return;
    const legal = R.moves(s);
    const direct = legal.find((m) => m.at === cell || m.dead === cell);
    if (direct) { apply(direct, 'human'); return; }
    const next = sel.concat([cell]);
    const ext = legal.filter((m) => m.path && sameCells(next, m.path));
    if (!ext.length) {
      sel = legal.some((m) => m.path && m.path[0] === cell) ? [cell] : [];
      render();
      return;
    }
    const done = ext.filter((m) => m.path.length === next.length);
    if (done.length && done.length === ext.length) {
      if (done.length > 1 && done[0].promo) { choosePromotion(done); return; }
      apply(done[0], 'human');
      return;
    }
    sel = next;
    render();
  }

  function choosePromotion(options) {
    const white = R.turn(cur()) === 0;
    const names = { q: 'Queen', r: 'Rook', b: 'Bishop', n: 'Knight' };
    const dlg = h('dialog', { class: 'promo', 'aria-label': 'Promote the pawn to' });
    dlg.append(h('header', null, h('h2', null, 'Promote the pawn to')),
      h('div', { class: 'body row' }, options.map((m) => h('button', { onclick: () => { dlg.close(); apply(m, 'human'); } },
        h('span', { class: `pc ${white ? 'w' : 'b'}` }, GLYPH[m.promo]), ' ', names[m.promo]))),
      h('div', { class: 'body row' }, h('button', { onclick: () => { dlg.close(); sel = []; render(); } }, 'Cancel')));
    dlg.addEventListener('close', () => dlg.remove());
    document.body.appendChild(dlg);
    dlg.showModal();
  }

  // ------------------------------------------------------------ controls
  const modeSel = h('select', { 'aria-label': 'Players', disabled: !!R.noComputer },
    h('option', { value: 'computer' }, 'Against the computer'), h('option', { value: 'two' }, 'Two players on this device'));
  const sideSel = h('select', { 'aria-label': 'You play' }, R.players.map((p, i) => h('option', { value: i }, `You play ${p}`)));
  const levelSel = h('select', { 'aria-label': 'Computer strength' },
    h('option', { value: 'easy' }, 'Easy'), h('option', { value: 'medium' }, 'Medium'), h('option', { value: 'hard' }, 'Hard'));
  modeSel.value = cfg.mode;
  sideSel.value = String(cfg.human);
  levelSel.value = cfg.level;
  const optSels = (g.options || []).map((o) => {
    const el = h('select', { 'aria-label': o.label }, o.values.map(([v, label]) => h('option', { value: v }, label)));
    el.value = String(cfg.opts[o.key] ?? o.values[0][0]);
    el.addEventListener('change', () => {
      if (history.length && !confirm('Start a new game with this board?')) { el.value = String(cfg.opts[o.key] ?? o.values[0][0]); return; }
      cfg.opts = { ...cfg.opts, [o.key]: Number(el.value) };
      start([]); persist(); render(); schedule();
    });
    return el;
  });
  const reconfigure = () => { clearTimeout(timer); thinking = false; sel = []; persist(); render(); schedule(); };
  modeSel.addEventListener('change', () => { cfg.mode = modeSel.value; reconfigure(); });
  sideSel.addEventListener('change', () => { cfg.human = Number(sideSel.value); reconfigure(); });
  levelSel.addEventListener('change', () => { cfg.level = levelSel.value; persist(); });
  const newBtn = h('button', { onclick: () => {
    if (history.length && !R.result(cur()) && !confirm('Abandon this game and start a new one?')) return;
    clearTimeout(timer); thinking = false; start([]); persist(); render(); schedule();
  } }, 'New game');
  const undoBtn = h('button', { onclick: undo }, 'Undo');
  const status = h('p', { class: 'game-status', role: 'status', 'aria-live': 'polite' });
  const actions = h('div', { class: 'row game-actions' });
  const boardBox = h('div', { class: `board-box ${g.id}` });
  const info = h('div', { class: 'game-info small' });
  const rules = h('details', { class: 'game-rules' }, h('summary', null, 'How to play'), g.howTo.map((p) => h('p', null, p)));

  main.appendChild(h('section', { class: 'panel game' },
    h('header', null, h('a', { class: 'button', href: '#/entertainment' }, '← Entertainment'), h('h2', null, g.title),
      h('div', { class: 'row' }, modeSel, sideSel, levelSel, optSels, undoBtn, newBtn)),
    h('div', { class: 'body game-body' },
      h('div', { class: 'game-main' }, status, boardBox, actions),
      h('aside', { class: 'game-side' }, info, rules))));

  // ------------------------------------------------------------ render
  function who(p) {
    if (cfg.mode !== 'computer') return R.players[p];
    return p === cfg.human ? `You (${R.players[p]})` : `The computer (${R.players[p]})`;
  }

  function statusText(s) {
    const r = R.result(s);
    if (r) {
      if (r.winner === null || r.winner === undefined) return `Draw: ${r.reason}.`;
      const winner = cfg.mode === 'computer' ? (r.winner === cfg.human ? 'You win' : 'The computer wins') : `${R.players[r.winner]} wins`;
      return `${winner}: ${r.reason}${r.points ? ` (${r.points} point${r.points === 1 ? '' : 's'})` : ''}.`;
    }
    const t = R.turn(s);
    if (R.chance) {
      if (s.phase === 'opening') return s.note === 'tie' ? `Both rolled ${s.rolled[0]}: roll again.` : 'Each player rolls one die; the higher number starts.';
      const prev = s.note === 'blocked' ? `${who(1 - t)} rolled ${s.rolled.join('–')} and cannot move. ` : '';
      if (s.phase === 'roll') return `${prev}${who(t)} to roll.`;
      return `${who(t)} to move: ${s.dice.join(', ')} left.`;
    }
    if (g.id === 'go' && s.phase === 'scoring') {
      const sc = goScore(s);
      return `Both passed. Tap groups that are dead, then accept. Black ${sc.black}, White ${sc.white}.`;
    }
    let text = thinking && !humansTurn() ? `${who(t)} is thinking…` : `${who(t)} to move.`;
    if (g.id === 'chess' && inCheck(s)) text = `Check. ${text}`;
    if (R.moves(s).length === 1 && R.moves(s)[0].pass) text = `${who(t)} has no move and must pass.`;
    return text;
  }

  function render() {
    if (!alive) return;
    const s = cur();
    sideSel.hidden = levelSel.hidden = cfg.mode !== 'computer';
    undoBtn.disabled = !canUndo() || thinking;
    status.textContent = statusText(s);
    const legal = humansTurn() && !R.result(s) && !thinking ? R.moves(s) : [];
    const hints = {
      sel: new Set(sel),
      targets: new Set(legal.filter((m) => m.path && sameCells(sel, m.path) && m.path.length > sel.length && sel.length).map((m) => m.path[sel.length])),
      starts: new Set(legal.filter((m) => m.path).map((m) => m.path[0])),
      at: new Set(legal.filter((m) => m.at !== undefined).map((m) => m.at)),
      flip: cfg.mode === 'computer' && cfg.human === 1,
    };
    fill(boardBox, RENDER[g.id](s, hints, onCell));
    fill(actions, ...actionButtons(s, legal));
    fill(info, ...INFO[g.id](s, states));
  }

  function actionButtons(s, legal) {
    const out = [];
    if (R.chance && !R.result(s) && (s.phase === 'opening' || (s.phase === 'roll' && humansTurn()))) {
      out.push(h('button', { class: 'primary', onclick: () => apply({ roll: rollDice() }, 'dice') }, s.phase === 'opening' ? 'Roll to start' : 'Roll the dice'));
    }
    if (R.chance && s.rolled && (s.phase === 'move' || s.note === 'blocked')) out.push(diceView(s));
    if (R.chance && s.phase === 'opening' && s.rolled) out.push(h('span', { class: 'small muted' }, `${R.players[0]} ${s.rolled[0]}, ${R.players[1]} ${s.rolled[1]}`));
    const pass = legal.find((m) => m.pass);
    if (pass) out.push(h('button', { class: g.id === 'reversi' ? 'primary' : null, onclick: () => apply(pass, 'human') }, 'Pass'));
    const accept = legal.find((m) => m.accept);
    if (accept) out.push(h('button', { class: 'primary', onclick: () => apply(accept, 'human') }, 'Accept the score'));
    const resume = legal.find((m) => m.resume);
    if (resume) out.push(h('button', { onclick: () => apply(resume, 'human') }, 'Resume play'));
    if (sel.length) out.push(h('button', { onclick: () => { sel = []; render(); } }, 'Cancel selection'));
    return out;
  }

  start(saved.moves);
  render();
  schedule();
  return () => { alive = false; clearTimeout(timer); };
}

// ------------------------------------------------------------ boards
const GLYPH = { k: '♚', q: '♛', r: '♜', b: '♝', n: '♞', p: '♟︎' };
const NAMES = { k: 'king', q: 'queen', r: 'rook', b: 'bishop', n: 'knight', p: 'pawn' };

function cellButton(cls, label, onclick) {
  return h('button', { class: cls, 'aria-label': label, onclick, type: 'button' });
}

function chessBoard(s, hint, onCell) {
  const grid = h('div', { class: 'grid8 chessboard', role: 'grid', 'aria-label': 'Chess board' });
  const last = s.last ? [s.last.from, s.last.to] : [];
  const check = inCheck(s) ? s.kings[s.side] : -1;
  for (let i = 0; i < 64; i++) {
    const row = hint.flip ? Math.floor(i / 8) : 7 - Math.floor(i / 8);
    const file = hint.flip ? 7 - (i % 8) : i % 8;
    const sq = row * 16 + file;
    const p = s.board[sq];
    const cls = ['sq', (row + file) % 2 ? 'light' : 'dark'];
    if (hint.sel.has(sq)) cls.push('sel');
    if (hint.targets.has(sq)) cls.push(p ? 'target capture' : 'target');
    if (last.includes(sq)) cls.push('last');
    if (sq === check) cls.push('check');
    const white = p && p === p.toUpperCase();
    const label = `${squareName(sq)}${p ? `, ${white ? 'white' : 'black'} ${NAMES[p.toLowerCase()]}` : ''}`;
    const b = cellButton(cls.join(' '), label, () => onCell(sq));
    if (p) b.appendChild(h('span', { class: `pc ${white ? 'w' : 'b'}` }, GLYPH[p.toLowerCase()]));
    if (i % 8 === 0) b.appendChild(h('span', { class: 'coord rank' }, String(row + 1)));
    if (i >= 56) b.appendChild(h('span', { class: 'coord file' }, 'abcdefgh'[file]));
    grid.appendChild(b);
  }
  return grid;
}

function checkersBoard(s, hint, onCell) {
  const grid = h('div', { class: 'grid8 checkerboard', role: 'grid', 'aria-label': 'Checkers board' });
  const last = s.last ? s.last.path : [];
  for (let i = 0; i < 64; i++) {
    const sq = hint.flip ? 63 - i : i;
    const dark = ((sq >> 3) + (sq & 7)) % 2 === 1;
    if (!dark) { grid.appendChild(h('span', { class: 'sq light' })); continue; }
    const p = s.board[sq];
    const cls = ['sq', 'dark'];
    if (hint.sel.has(sq)) cls.push('sel');
    if (hint.targets.has(sq)) cls.push('target');
    if (!hint.sel.size && hint.starts.has(sq)) cls.push('can');
    if (last.includes(sq)) cls.push('last');
    const label = `row ${(sq >> 3) + 1}, column ${(sq & 7) + 1}${p ? `, ${p.toLowerCase() === 'd' ? 'dark' : 'light'} ${p === p.toUpperCase() ? 'king' : 'piece'}` : ''}`;
    const b = cellButton(cls.join(' '), label, () => onCell(sq));
    if (p) b.appendChild(h('span', { class: `disc ${p.toLowerCase() === 'd' ? 'd' : 'l'}${p === p.toUpperCase() ? ' king' : ''}` }, p === p.toUpperCase() ? '♛' : ''));
    grid.appendChild(b);
  }
  return grid;
}

function reversiBoard(s, hint, onCell) {
  const grid = h('div', { class: 'grid8 reversiboard', role: 'grid', 'aria-label': 'Reversi board' });
  const flipped = new Set(s.last && s.last.flips ? s.last.flips : []);
  for (let at = 0; at < 64; at++) {
    const v = s.board[at];
    const cls = ['sq'];
    if (hint.at.has(at)) cls.push('target');
    if (s.last && s.last.at === at) cls.push('last');
    const label = `${'abcdefgh'[at & 7]}${(at >> 3) + 1}${v === null ? '' : v === 0 ? ', black' : ', white'}`;
    const b = cellButton(cls.join(' '), label, () => onCell(at));
    if (v !== null) b.appendChild(h('span', { class: `disc ${v === 0 ? 'b' : 'w'}${flipped.has(at) ? ' turned' : ''}` }));
    grid.appendChild(b);
  }
  return grid;
}

function fourBoard(s, hint, onCell) {
  const grid = h('div', { class: 'grid-four', role: 'grid', 'aria-label': 'Four in a row grid' });
  const line = new Set(winLine(s.board) || []);
  for (let i = 0; i < ROWS * COLS; i++) {
    const col = i % COLS;
    const v = s.board[i];
    const cls = ['slot'];
    if (hint.at.has(col)) cls.push('open');
    if (line.has(i)) cls.push('win');
    if (s.last && s.last.cell === i) cls.push('last');
    const b = cellButton(cls.join(' '), `column ${col + 1}${v === null ? '' : v === 0 ? ', red' : ', yellow'}`, () => onCell(col));
    if (v !== null) b.appendChild(h('span', { class: `disc ${v === 0 ? 'red' : 'yellow'}` }));
    grid.appendChild(b);
  }
  return grid;
}

function mancalaBoard(s, hint, onCell) {
  const north = hint.flip ? [0, 1, 2, 3, 4, 5].reverse() : [12, 11, 10, 9, 8, 7];
  const south = hint.flip ? [7, 8, 9, 10, 11, 12] : [0, 1, 2, 3, 4, 5];
  const [leftStore, rightStore] = hint.flip ? [6, 13] : [13, 6];
  const pit = (i) => {
    const cls = ['pit'];
    if (hint.at.has(i)) cls.push('can');
    if (s.last && s.last.at === i) cls.push('last');
    if (s.last && s.last.end === i) cls.push('end');
    const b = cellButton(cls.join(' '), `${i <= 5 ? 'South' : 'North'} pit with ${s.pits[i]} seeds`, () => onCell(i));
    b.appendChild(h('span', { class: 'seeds' }, String(s.pits[i])));
    return b;
  };
  const storeEl = (i) => h('div', { class: `store${s.last && s.last.end === i ? ' end' : ''}`, 'aria-label': `${i === 6 ? 'South' : 'North'} store, ${s.pits[i]} seeds` },
    h('span', { class: 'seeds' }, String(s.pits[i])), h('span', { class: 'small' }, i === 6 ? 'South' : 'North'));
  return h('div', { class: 'mancala-board' }, storeEl(leftStore),
    h('div', { class: 'pits' }, h('div', { class: 'pit-row' }, north.map(pit)), h('div', { class: 'pit-row' }, south.map(pit))),
    storeEl(rightStore));
}

function backgammonBoard(s, hint, onCell) {
  // White's view: top row 13–24 left to right, bottom row 12–1.
  let top = [13, 14, 15, 16, 17, 18, 19, 20, 21, 22, 23, 24];
  let bottom = [12, 11, 10, 9, 8, 7, 6, 5, 4, 3, 2, 1];
  // Black's view is the mirror image: its home board, 19–24, at the bottom right.
  if (hint.flip) { top = [12, 11, 10, 9, 8, 7, 6, 5, 4, 3, 2, 1]; bottom = [13, 14, 15, 16, 17, 18, 19, 20, 21, 22, 23, 24]; }
  const last = s.last && s.last.path ? s.last.path : [];
  const point = (n, up) => {
    const c = s.pts[n];
    const cls = ['point', up ? 'up' : 'down', n % 2 ? 'odd' : 'even'];
    if (hint.sel.has(n)) cls.push('sel');
    if (hint.targets.has(n)) cls.push('target');
    if (!hint.sel.size && hint.starts.has(n)) cls.push('can');
    if (last.includes(n)) cls.push('last');
    const b = cellButton(cls.join(' '), `point ${n}${c ? `, ${Math.abs(c)} ${c > 0 ? 'white' : 'black'}` : ''}`, () => onCell(n));
    b.appendChild(h('span', { class: 'num-label' }, String(n)));
    const stack = h('span', { class: 'stack' });
    for (let k = 0; k < Math.min(Math.abs(c), 5); k++) stack.appendChild(h('span', { class: `chk ${c > 0 ? 'w' : 'b'}` }));
    if (Math.abs(c) > 5) stack.appendChild(h('span', { class: 'more' }, String(Math.abs(c))));
    b.appendChild(stack);
    return b;
  };
  const bar = cellButton(['bar', hint.sel.has('bar') ? 'sel' : '', !hint.sel.size && hint.starts.has('bar') ? 'can' : ''].join(' '),
    `bar: ${s.bar[0]} white, ${s.bar[1]} black`, () => onCell('bar'));
  bar.append(h('span', { class: 'bar-count b' }, s.bar[1] ? `${s.bar[1]}` : ''), h('span', { class: 'bar-count w' }, s.bar[0] ? `${s.bar[0]}` : ''));
  const off = cellButton(['off', hint.targets.has('off') ? 'target' : ''].join(' '), `borne off: ${s.off[0]} white, ${s.off[1]} black`, () => onCell('off'));
  off.append(h('span', { class: 'off-count' }, `${s.off[1]}`), h('span', { class: 'small' }, 'off'), h('span', { class: 'off-count' }, `${s.off[0]}`));
  return h('div', { class: 'bg-board' },
    h('div', { class: 'bg-half top' }, top.slice(0, 6).map((n) => point(n, false))), bar,
    h('div', { class: 'bg-half top right' }, top.slice(6).map((n) => point(n, false))), off,
    h('div', { class: 'bg-half bottom' }, bottom.slice(0, 6).map((n) => point(n, true))),
    h('div', { class: 'bg-half bottom right' }, bottom.slice(6).map((n) => point(n, true))));
}

function diceView(s) {
  const left = s.dice.slice();
  const shown = s.rolled[0] === s.rolled[1] ? [s.rolled[0], s.rolled[0], s.rolled[0], s.rolled[0]] : s.rolled;
  return h('span', { class: 'dice', 'aria-label': `Dice ${s.rolled.join(' and ')}` }, shown.map((d) => {
    const i = left.indexOf(d);
    const used = s.phase !== 'move' || i < 0;
    if (!used) left.splice(i, 1);
    return h('span', { class: `die${used ? ' used' : ''}` }, '⚀⚁⚂⚃⚄⚅'[d - 1]);
  }));
}

const STARS = { 9: [2, 4, 6], 13: [3, 6, 9], 19: [3, 9, 15] };

function goBoard(s, hint, onCell) {
  const n = s.size;
  const grid = h('div', { class: 'grid-go', role: 'grid', 'aria-label': `Go board, ${n} by ${n}`, style: `--n:${n}` });
  const dead = new Set(s.dead);
  const terr = s.phase !== 'play' ? goScore(s).territory : null;
  const stars = STARS[n];
  for (let p = 0; p < n * n; p++) {
    const r = Math.floor(p / n);
    const c = p % n;
    const v = s.board[p];
    const cls = ['pt'];
    if (r === 0) cls.push('top'); if (r === n - 1) cls.push('bottom'); if (c === 0) cls.push('left'); if (c === n - 1) cls.push('right');
    // Star points: corners and centre (and the sides on 19×19).
    if (v === null && stars.includes(r) && stars.includes(c) && (n === 19 || (r !== stars[1]) === (c !== stars[1]))) cls.push('star');
    if (hint.at.has(p)) cls.push('open');
    const b = cellButton(cls.join(' '), `${'ABCDEFGHJKLMNOPQRST'[c]}${n - r}${v === null ? '' : v === 0 ? ', black' : ', white'}${dead.has(p) ? ' (dead)' : ''}`, () => onCell(p));
    if (v !== null) b.appendChild(h('span', { class: `stone ${v === 0 ? 'b' : 'w'}${dead.has(p) ? ' dead' : ''}${s.last && s.last.at === p ? ' last' : ''}` }));
    else if (terr && terr[p] !== null) b.appendChild(h('span', { class: `terr ${terr[p] === 0 ? 'b' : 'w'}` }));
    grid.appendChild(b);
  }
  return grid;
}

const RENDER = { chess: chessBoard, checkers: checkersBoard, reversi: reversiBoard, fourinarow: fourBoard, mancala: mancalaBoard, backgammon: backgammonBoard, go: goBoard };

// ------------------------------------------------------------ side panel
function chessMoves(states) {
  const list = h('ol', { class: 'move-list' });
  for (let i = 1; i < states.length; i += 2) {
    const w = san(states[i - 1], states[i].last);
    const b = i + 1 < states.length ? san(states[i], states[i + 1].last) : '';
    list.appendChild(h('li', null, h('span', null, w), ' ', h('span', null, b)));
  }
  return list;
}

const INFO = {
  chess: (s, states) => [h('h3', null, 'Moves'), states.length > 1 ? chessMoves(states) : h('p', { class: 'muted' }, 'No moves yet.')],
  checkers: (s) => {
    const n = [0, 0];
    for (const p of s.board) if (p) n[p.toLowerCase() === 'd' ? 0 : 1]++;
    return [h('p', null, `Dark ${n[0]} pieces, Light ${n[1]}.`), h('p', { class: 'muted' }, 'Pieces that can move are outlined. Capturing is compulsory.')];
  },
  reversi: (s) => { const [b, w] = reversiCount(s); return [h('p', null, `Black ${b} discs, White ${w}.`)]; },
  fourinarow: () => [],
  mancala: (s) => [h('p', null, `South's store ${s.pits[6]}, North's store ${s.pits[13]}.`),
    s.last && s.last.captured ? h('p', null, `Last move captured ${s.last.captured} seeds.`) : null,
    s.last && s.last.again ? h('p', null, 'The last seed landed in the store: another turn.') : null],
  backgammon: (s) => [h('p', null, `Pip count: White ${pips(s, 0)}, Black ${pips(s, 1)}.`),
    h('p', { class: 'muted' }, 'White moves from 24 towards 1 and bears off at the right; Black moves from 1 towards 24.')],
  go: (s) => [h('p', null, `Captured: by Black ${s.captured[0]}, by White ${s.captured[1]}.`), h('p', { class: 'muted' }, `Komi ${7.5} for White.`)],
};


// The board games built into Arcana: what the Entertainment page lists and
// what the game page needs to know about each. Everything here runs on the
// device showing the game; nothing is sent to the server.
import * as chess from './chess.js';
import * as checkers from './checkers.js';
import * as backgammon from './backgammon.js';
import * as reversi from './reversi.js';
import * as fourinarow from './fourinarow.js';
import * as mancala from './mancala.js';
import * as go from './go.js';

// levels: how long the computer thinks (ms), how deep it may look, and how
// much noise blurs its judgement, in the units of each game's evaluation.
export const GAMES = [
  {
    id: 'chess', title: 'Chess', rules: chess, players: '2 players, or against the computer',
    blurb: 'The classic game of kings, queens and checkmate.',
    levels: { easy: { timeMs: 200, maxDepth: 2, noise: 220 }, medium: { timeMs: 600, maxDepth: 3, noise: 40 }, hard: { timeMs: 2000, maxDepth: 64, noise: 0 } },
    howTo: [
      'White moves first. Each side has a king, a queen, two rooks, two bishops, two knights and eight pawns.',
      'The king moves one square in any direction; the queen any distance along a line or diagonal; rooks along lines; bishops along diagonals. Knights jump in an L shape: two squares one way and one to the side. Pawns move one square forward (two from their first square) and capture one square diagonally forward.',
      'A pawn that reaches the far side becomes a queen, rook, bishop or knight. A pawn that has just moved two squares can be taken en passant by a pawn beside it, as if it had moved one.',
      'Castling: the king moves two squares towards a rook, which jumps over it. Neither may have moved, the squares between must be empty, and the king may not be in check, pass through check or land in check.',
      'Attacking the king is check, and it must be answered at once. If it cannot be, that is checkmate and the game is lost. A player with no legal move who is not in check is stalemated: a draw. Three repetitions of a position, fifty moves each without a capture or pawn move, and too little material to mate are also draws.',
      'Choose a piece, then the square to move it to.',
    ],
  },
  {
    id: 'checkers', title: 'Checkers', rules: checkers, players: '2 players, or against the computer',
    blurb: 'English draughts: jump your opponent’s pieces, crown your own.',
    levels: { easy: { timeMs: 150, maxDepth: 2, noise: 120 }, medium: { timeMs: 500, maxDepth: 6, noise: 20 }, hard: { timeMs: 1500, maxDepth: 64, noise: 0 } },
    howTo: [
      'Dark moves first. Pieces move one square diagonally forward onto an empty dark square.',
      'A piece captures by jumping over an opponent’s piece next to it onto the empty square beyond. Capturing is compulsory, and a piece that can jump again must keep jumping in the same move.',
      'A piece that reaches the far row is crowned king, and its move ends there. Kings move and jump diagonally forwards and backwards.',
      'You win when your opponent cannot move: all their pieces are taken or blocked. Forty moves each without a capture or an uncrowned piece moving is a draw.',
      'Choose a piece, then each square it lands on.',
    ],
  },
  {
    id: 'backgammon', title: 'Backgammon', rules: backgammon, players: '2 players, or against the computer',
    blurb: 'Race your fifteen checkers home and bear them off, without the doubling cube.',
    levels: { easy: { noise: 60 }, medium: { noise: 12 }, hard: { noise: 0 } },
    howTo: [
      'Each player rolls one die to start; the higher number moves first, using both dice. After that, each turn starts with a roll of two dice.',
      'White moves towards point 1 and Black towards point 24, each a number of points equal to a die. Each die is a separate move, by the same checker or another. Doubles are played four times. You must use both dice if you can; if only one can be used, use the higher if possible.',
      'A checker may not land on a point held by two or more of the opponent’s checkers. Landing on a single opposing checker (a blot) hits it: it goes to the bar and must re-enter in its owner’s opponent’s home board before its owner can move anything else.',
      'Once all fifteen of your checkers are in your home board, you can bear them off with the die that matches their point, or a higher die from your highest point. The first to bear off all fifteen wins: a gammon (two points) if the loser has borne off none, a backgammon (three) if the loser also still has a checker on the bar or in the winner’s home board.',
      'Choose a checker, then where it goes. The pip count says how far each side still has to travel.',
    ],
  },
  {
    id: 'reversi', title: 'Reversi', rules: reversi, players: '2 players, or against the computer',
    blurb: 'Outflank lines of discs and turn them to your colour.',
    levels: { easy: { timeMs: 150, maxDepth: 2, noise: 60 }, medium: { timeMs: 500, maxDepth: 4, noise: 10 }, hard: { timeMs: 1500, maxDepth: 64, noise: 0 } },
    howTo: [
      'Black moves first. Place a disc so that one or more straight lines of your opponent’s discs lie between it and another of your discs. Those discs turn over to your colour.',
      'If you cannot turn any disc you must pass. The game ends when neither player can move, and the player with more discs wins.',
      'Corners can never be turned over, so they are worth having.',
    ],
  },
  {
    id: 'fourinarow', title: 'Four in a row', rules: fourinarow, players: '2 players, or against the computer',
    blurb: 'Drop discs into the grid and line up four.',
    levels: { easy: { timeMs: 100, maxDepth: 2, noise: 40 }, medium: { timeMs: 400, maxDepth: 5, noise: 8 }, hard: { timeMs: 1500, maxDepth: 64, noise: 0 } },
    howTo: [
      'Players take turns dropping a disc into one of the seven columns; it falls to the lowest empty space.',
      'The first to line up four of their discs across, up and down or diagonally wins. If the grid fills up first, the game is a draw.',
    ],
  },
  {
    id: 'mancala', title: 'Mancala (Kalah)', rules: mancala, players: '2 players, or against the computer',
    blurb: 'Sow seeds around the board and capture more than your opponent.',
    levels: { easy: { timeMs: 150, maxDepth: 2, noise: 60 }, medium: { timeMs: 500, maxDepth: 6, noise: 10 }, hard: { timeMs: 1500, maxDepth: 64, noise: 0 } },
    howTo: [
      'Each player has six pits on their side and a store on their right. Every pit starts with four seeds.',
      'On your turn, pick up all the seeds in one of your pits and sow them one at a time into the following pits, counter-clockwise, including your own store but not your opponent’s.',
      'If the last seed lands in your store, you move again. If it lands in an empty pit on your side, you capture it and all the seeds in the pit opposite, if there are any.',
      'When one player’s pits are all empty, the other player puts the seeds left on their side into their store. The player with more seeds in their store wins.',
    ],
  },
  {
    id: 'go', title: 'Go', rules: go, players: '2 players on one device',
    blurb: 'Surround territory with stones on a 9×9, 13×13 or 19×19 board.',
    options: [{ key: 'size', label: 'Board', values: [[9, '9 × 9'], [13, '13 × 13'], [19, '19 × 19']] }],
    howTo: [
      'Black plays first. Players take turns placing a stone on an empty point where the lines cross.',
      'Stones next to each other along the lines form a group. A group with no empty point next to it is captured and removed. You may not place a stone where your own group would have no empty point next to it, unless it captures.',
      'Ko: you may not recapture at once in a way that brings back the position after your previous move. Play elsewhere first.',
      'When neither player can gain anything, both pass in turn. Then tap groups that could not escape capture to mark them dead, and accept the score.',
      'The score is the number of your stones on the board plus the empty points that only your stones surround. White adds 7.5 points (komi) for moving second. The higher score wins.',
    ],
  },
];

export const byId = Object.fromEntries(GAMES.map((g) => [g.id, g]));

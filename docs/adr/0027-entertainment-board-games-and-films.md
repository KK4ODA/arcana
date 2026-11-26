# ADR-0027: Entertainment: built-in board games and public-domain films

- Status: Accepted
- Date: 2026-11-26

## Context
The owner asked for entertainment in Arcana, naming board games and
films. A library carried on a drive, or served from the appliance to a
household or a shelter, is used during long outages, when people also need
something to do in the evenings. Arcana had no category for this, played no
video in its interface, and offered no games.

**Board games.** Kiwix archives contain no playable games. Open-source web
games exist (for example chess.js with a board component), but they arrive
through npm, which the web interface does not use (ADR-0014), and each
brings its own look and licence notices. Traditional games have simple,
fixed rules that fit in a few hundred lines each.

**Films.** Three sources were evaluated:
- **Internet Archive, Feature Films collection.** Many films the US public
  domain is documented for, each with a 640×480 H.264 MP4 derivative of
  0.4–0.8 GB that every current browser plays, range requests, and SHA-1
  and size in the item metadata. Uploaders mark items public domain; the
  collection also holds items that are not.
- **Wikimedia Commons.** Community licence review, but films are WebM (VP9)
  or Ogg Theora, which older iPhones do not play, and the smaller
  transcodes have no published checksum.
- **Kiwix video archives.** TED talks (restricted to personal viewing),
  YouTube channel archives and Blender Studio's open films. They are
  already in the catalog; only their category was wrong.

## Decision
- **A category, `entertainment`** ("Films, games and entertainment"). Kiwix
  archives about board games, chess, puzzles, role-playing games, films and
  television, comics and sports map to it by name (`kiwixCategory`), as do
  Blender Studio's open films. Their licences are unchanged by this.
- **Board games are built in, not downloaded.** Seven traditional games with
  no owner: chess, checkers (English draughts), backgammon (without the
  doubling cube), reversi, four in a row, mancala (Kalah, six pits and four
  seeds) and Go (9×9, 13×13, 19×19, area scoring, 7.5 komi). Trademarked
  names (Othello, Connect Four) are not used.
  - Each game's rules are a pure ES module in `static/js/games/` with no
    imports (`newGame`, `turn`, `moves`, `play`, `result`, `evaluate`).
    States are never modified; `play` returns a new one. `engine.js`
    searches any of them with negamax, alpha-beta pruning and iterative
    deepening under a time budget, with quiescence over captures where a
    game provides them. Three levels set the time, depth and a random blur
    of the evaluation. Backgammon chooses whole turns by evaluating every
    legal way to play the dice. Go has no computer opponent.
  - Games run entirely in the browser showing them: two players on one
    device, or against the computer. The game in progress is kept in that
    browser's local storage, so network visitors play exactly as the host
    does and the server stores nothing. Undo against the computer takes
    back the computer's reply and the player's move; dice rolls cannot be
    undone. Dice come from `crypto.getRandomValues` without modulo bias.
  - The rules are tested with Node's built-in test runner
    (`internal/web/testdata/games`), started by `go test` when Node is
    installed and skipped otherwise. Chess move generation is checked by
    perft against five standard positions (up to 97,862 positions deep).
- **Films are curated catalog entries** in the bundled catalog, format
  `video`, category `entertainment/films`, pointing at the Internet
  Archive's H.264 derivative. Each file's SHA-256 was measured by streaming
  it once at review time, which also confirmed the archive's published
  size and SHA-1; downloads are verified against that SHA-256 like any
  other bundled entry (ADR-0009). Update strategy is `none`.
  - A film is listed only if its US public-domain status is documented
    (copyright not renewed, or released without a valid notice) and the
    archive publishes it as public domain. Each licence record says which,
    and that status in other countries differs.
  - Silent films are left out: the copies on the archive carry scores added
    later whose rights could not be established. Films built around songs
    published separately after 1930 are left out (Royal Wedding, Penny
    Serenade). Where a film's music was also published on its own and its
    renewal was not checked, the licence notes say so (Gulliver's Travels,
    Jungle Book).
  - Content notes use the existing `content` warning: horror and graphic
    violence, crime for adults, documentary footage of Nazi camps, and
    stereotyped portrayals of their time.
  - Thirteen films ship, 7.4 GB together. A collection, *Films and Games*,
    and a Getting started topic offer them by priority with the board-game
    and chess question-and-answer archives.
- **Playback in the interface.** A film plays in a `<video>` element on the
  application origin, loaded from the content origin; the application's
  CSP gains `media-src <content origin>` and nothing else. The content
  origin already serves byte ranges (`http.ServeContent`), so players can
  seek. WebM is now served as video too. Where each viewer stopped is kept
  in that browser's local storage and offered on return.

## Consequences
- Entertainment works offline on every device that can show Arcana,
  including phones on the network, with no new Go module and no npm. The
  games add about 90 kB of JavaScript to the binary.
- The acceptance test's browser tripwire opens the Entertainment page and
  every game, at desktop and phone width.
- Not done: games between two devices over the network (it needs a write
  path for network visitors, who are read-only today; ADR-0017), subtitles
  (the archive copies have none), transcoding, playlists, and the owner's
  own video files shared to network visitors (user documents are private
  to the host computer; a shared media folder is a separate decision).
- Not verified: the computer opponent's strength beyond the tests (it finds
  mate in one, avoids losing its queen to a defended pawn, takes and blocks
  four in a row) and play on real phones and Safari; checked in Chromium at
  1280 px and 390 px.
- Film entries depend on the archive keeping the same derivative files. If
  one is regenerated, its download fails verification and the entry must be
  re-measured.

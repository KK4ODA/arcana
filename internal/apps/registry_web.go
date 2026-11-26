package apps

// The web apps: prebuilt sites Arcana serves from their unpacked files.

func init() {
	register(
		&Spec{
			ID: "cyberchef", Title: "CyberChef", Kind: KindWeb, Slot: 3,
			Summary: "Encode, decode, encrypt, hash and analyse data in the browser.",
			Parts:   map[string]Part{"cyberchef": {}},
			Root:    "cyberchef", Index: "CyberChef_v10.24.0.html",
		},
		&Spec{
			ID: "it-tools", Title: "IT Tools", Kind: KindWeb, Slot: 4,
			Summary: "Converters, generators and calculators for computer and network work.",
			Parts:   map[string]Part{"it-tools": {}},
			Root:    "it-tools/dist", SPA: true,
		},
		&Spec{
			ID: "excalidraw", Title: "Excalidraw", Kind: KindWeb, Slot: 5,
			Summary: "A whiteboard for sketches, diagrams and plans.",
			Parts:   map[string]Part{"excalidraw": {}},
			Root:    "excalidraw/usr/share/nginx/html",
		},
		&Spec{
			ID: "meshtastic-web", Title: "Meshtastic Web", Kind: KindWeb, Slot: 6,
			Summary: "Message through a Meshtastic radio, and change its settings, over USB or Bluetooth.",
			Parts:   map[string]Part{"meshtastic-web": {Gunzip: true}},
			Root:    "meshtastic-web", SPA: true,
			SecureContext: "Browsers allow USB (Web Serial) and Bluetooth only on a secure page: open it on the computer running Arcana, or through Arcana's HTTPS address. Chrome and Edge support both; Firefox and Safari support neither.",
		},
		&Spec{
			// Closed source: downloaded from its author on request, never
			// shipped with Arcana (ADR-0030).
			ID: "meshcore-web", Title: "MeshCore Web", Kind: KindWeb, Slot: 13,
			Summary: "Message through a MeshCore radio, and manage its contacts and channels, over USB or Bluetooth.",
			Parts:   map[string]Part{"meshcore-web": {}, "fonts": {}},
			Root:    "meshcore-web/web",
			// Flutter loads its rendering engine and fonts from Google's
			// servers by default, which the CSP blocks (and which are not
			// there offline): use the engine the build carries, and fonts
			// Arcana downloaded with the app.
			Rewrites: []Rewrite{
				{Path: "/index.html", Old: meshcoreConfig, New: meshcoreLocal},
				{Path: "/flutter_bootstrap.js", Old: meshcoreConfig, New: meshcoreLocal},
			},
			Extra: map[string]string{
				"/arcana-fonts/roboto/v32/KFOmCnqEu92Fr1Me4GZLCzYlKw.woff2":                                          "fonts/roboto-v32.woff2",
				"/arcana-fonts/notosanssymbols/v43/rP2up3q65FkAtHfwd-eIS2brbDN6gxP34F9jRRCe4W3gfQ8gb_VFRkzrbQ.woff2": "fonts/notosanssymbols-v43.woff2",
			},
			SecureContext: "Browsers allow USB (Web Serial) and Bluetooth only on a secure page: open it on the computer running Arcana, or through Arcana's HTTPS address. Chrome and Edge support both; Firefox and Safari support neither.",
		},
	)
}

// Flutter's start-up code in MeshCore Web's index.html and
// flutter_bootstrap.js. The engine reads the fonts' address from
// window.flutterConfiguration, and where to find its engine from the build
// configuration.
const (
	meshcoreConfig = `_flutter.buildConfig = {"engineRevision"`
	meshcoreLocal  = `window.flutterConfiguration = {fontFallbackBaseUrl: "/arcana-fonts/"};` +
		`_flutter.buildConfig = {"useLocalCanvasKit":true,"engineRevision"`
)

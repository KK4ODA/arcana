package apps

// The web apps: prebuilt sites Arcana serves from their unpacked files.
// Project NOMAD's MeshCore Web is not here: the MeshCore app is closed
// source and published without a license to redistribute it (ADR-0030).

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
	)
}

package paths

const (
	DefaultDataDir        = "/var/lib/trustpanel"
	DefaultConfigDir      = "/etc/trustpanel"
	DefaultTrustTunnelDir = "/etc/trusttunnel"
	DefaultPanelListen    = "127.0.0.1:8787"
	DefaultFallbackListen = "127.0.0.1:8080"
	DefaultBotListen      = "127.0.0.1:8791"

	// DefaultQRPath is the unguessable, unlisted path on the camouflage origin
	// where the client-side deep-link/QR renderer is served (see
	// internal/fallback/qrpage.go). The panel builds landing links against it.
	// Overridable on the fallback host via FALLBACK_QR_PATH.
	DefaultQRPath = "/_int/tlv-probe-cf259edd5fab4e2f61b367c6"
)

package agent

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
)

// Certificate role markers: the panel presents a per-host cert with the
// controller role; agents present a node-role cert. Role is carried in the
// certificate subject's OrganizationalUnit.
const (
	RoleController = "controller"
	RoleNode       = "node"
)

// BuildAgentTLSConfig returns a TLS config for the agent's listener: it requires
// and verifies a client certificate signed by the fleet CA, and additionally
// requires the peer to carry the controller role.
func BuildAgentTLSConfig(caPEM []byte, serverCert tls.Certificate) (*tls.Config, error) {
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caPEM) {
		return nil, fmt.Errorf("no CA certificates parsed from caPEM")
	}
	return &tls.Config{
		MinVersion:   tls.VersionTLS13,
		Certificates: []tls.Certificate{serverCert},
		ClientCAs:    pool,
		ClientAuth:   tls.RequireAndVerifyClientCert,
		VerifyPeerCertificate: func(_ [][]byte, verifiedChains [][]*x509.Certificate) error {
			if len(verifiedChains) == 0 || len(verifiedChains[0]) == 0 {
				return fmt.Errorf("no verified client chain")
			}
			leaf := verifiedChains[0][0]
			if !hasRole(leaf, RoleController) {
				return fmt.Errorf("client certificate is not a controller (subject OU=%v)", leaf.Subject.OrganizationalUnit)
			}
			return nil
		},
	}, nil
}

// hasRole reports whether the certificate's subject carries the given role in
// its OrganizationalUnit list.
func hasRole(cert *x509.Certificate, role string) bool {
	for _, ou := range cert.Subject.OrganizationalUnit {
		if ou == role {
			return true
		}
	}
	return false
}

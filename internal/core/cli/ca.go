package cli

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"trustpanel/internal/core/pki"
)

// RunCA bootstraps the fleet PKI: init the CA and issue controller/node
// certs. Subcommands: init | controller | node | sign.
func RunCA(args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "usage: trustpanel ca init|controller|node|sign [flags]")
		os.Exit(2)
	}
	switch args[0] {
	case "init":
		caInit(args[1:])
	case "controller":
		caLeaf(args[1:], pki.RoleController)
	case "node":
		caLeaf(args[1:], pki.RoleNode)
	case "gen-csr":
		caGenCSR(args[1:])
	case "sign":
		caSign(args[1:])
	default:
		fmt.Fprintln(os.Stderr, "ca: unknown subcommand "+args[0])
		os.Exit(2)
	}
}

func caInit(args []string) {
	fs := flag.NewFlagSet("ca init", flag.ExitOnError)
	out := fs.String("out", ".", "output dir for ca.crt/ca.key")
	cn := fs.String("cn", "TrustPanel Fleet CA", "CA common name")
	days := fs.Int("days", 3650, "validity in days")
	_ = fs.Parse(args)

	certPEM, keyPEM, err := pki.GenerateCA(*cn, time.Duration(*days)*24*time.Hour)
	caMust(err)
	caWrite(filepath.Join(*out, "ca.crt"), certPEM, 0o644)
	caWrite(filepath.Join(*out, "ca.key"), keyPEM, 0o600)
	fmt.Printf("CA written to %s (ca.crt, ca.key)\n", *out)
}

func caLeaf(args []string, role string) {
	fs := flag.NewFlagSet("ca "+role, flag.ExitOnError)
	caDir := fs.String("ca", ".", "dir containing ca.crt/ca.key")
	out := fs.String("out", ".", "output dir")
	cn := fs.String("cn", "", "common name (defaults to role or node-id)")
	nodeID := fs.String("node-id", "", "node id (node role only)")
	ipsCSV := fs.String("ips", "", "comma-separated IP SANs")
	days := fs.Int("days", 90, "validity in days")
	_ = fs.Parse(args)

	ca, err := loadCA(*caDir)
	caMust(err)
	commonName := *cn
	if commonName == "" {
		if role == pki.RoleNode {
			commonName = *nodeID
		} else {
			commonName = "controller"
		}
	}
	if role == pki.RoleNode && *nodeID == "" {
		caFatal("node role requires --node-id")
	}
	certPEM, keyPEM, err := ca.IssueLeaf(role, commonName, parseIPList(*ipsCSV), time.Duration(*days)*24*time.Hour)
	caMust(err)
	prefix := role
	if role == pki.RoleNode {
		prefix = "node-" + *nodeID
	}
	caWrite(filepath.Join(*out, prefix+".crt"), certPEM, 0o644)
	caWrite(filepath.Join(*out, prefix+".key"), keyPEM, 0o600)
	caWrite(filepath.Join(*out, "ca.crt"), ca.CertPEM(), 0o644)
	fmt.Printf("%s cert written to %s (%s.crt, %s.key, ca.crt)\n", role, *out, prefix, prefix)
}

// caGenCSR runs on the NODE during enrollment: it generates the node keypair
// and a CSR locally so the private key never leaves the node. The CSR is
// printed to stdout (or written with --out) for the panel CA to sign.
func caGenCSR(args []string) {
	fs := flag.NewFlagSet("ca gen-csr", flag.ExitOnError)
	nodeID := fs.String("node-id", "", "node id (CSR common name)")
	out := fs.String("out", "", "dir to write node.key (+ node.csr); CSR also printed to stdout")
	_ = fs.Parse(args)

	if *nodeID == "" {
		caFatal("gen-csr requires --node-id")
	}
	csrPEM, keyPEM, err := pki.GenerateCSR(*nodeID)
	caMust(err)
	if *out != "" {
		caWrite(filepath.Join(*out, "node.key"), keyPEM, 0o600)
		caWrite(filepath.Join(*out, "node.csr"), csrPEM, 0o644)
	}
	// Always emit the CSR on stdout so a remote provisioner can capture it.
	fmt.Print(string(csrPEM))
}

func caSign(args []string) {
	fs := flag.NewFlagSet("ca sign", flag.ExitOnError)
	caDir := fs.String("ca", ".", "dir containing ca.crt/ca.key")
	out := fs.String("out", ".", "output dir")
	nodeID := fs.String("node-id", "", "node id (cert common name)")
	csrFile := fs.String("csr", "", "node-generated CSR PEM file")
	ipsCSV := fs.String("ips", "", "comma-separated IP SANs")
	days := fs.Int("days", 90, "validity in days")
	_ = fs.Parse(args)

	if *nodeID == "" || *csrFile == "" {
		caFatal("sign requires --node-id and --csr")
	}
	ca, err := loadCA(*caDir)
	caMust(err)
	csrPEM, err := os.ReadFile(*csrFile)
	caMust(err)
	certPEM, err := ca.SignCSR(csrPEM, pki.RoleNode, *nodeID, parseIPList(*ipsCSV), time.Duration(*days)*24*time.Hour)
	caMust(err)
	caWrite(filepath.Join(*out, "node-"+*nodeID+".crt"), certPEM, 0o644)
	caWrite(filepath.Join(*out, "ca.crt"), ca.CertPEM(), 0o644)
	fmt.Printf("signed node cert written to %s\n", *out)
}

// loadCA reads ca.crt/ca.key from dir and parses the fleet CA. Shared by the ca
// and provision subcommands; callers fatal in their own idiom.
func loadCA(dir string) (*pki.CA, error) {
	certPEM, err := os.ReadFile(filepath.Join(dir, "ca.crt"))
	if err != nil {
		return nil, err
	}
	keyPEM, err := os.ReadFile(filepath.Join(dir, "ca.key"))
	if err != nil {
		return nil, err
	}
	return pki.LoadCA(certPEM, keyPEM)
}

func caWrite(path string, data []byte, mode os.FileMode) {
	caMust(os.MkdirAll(filepath.Dir(path), 0o755))
	caMust(os.WriteFile(path, data, mode))
}

func caMust(err error) {
	if err != nil {
		caFatal(err.Error())
	}
}

func caFatal(msg string) {
	fmt.Fprintln(os.Stderr, "ca: "+msg)
	os.Exit(1)
}

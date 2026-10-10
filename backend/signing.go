package main

import (
	"bytes"
	"crypto"
	"encoding/hex"
	"net/http"
	"strings"

	"github.com/ProtonMail/go-crypto/openpgp"
	"github.com/ProtonMail/go-crypto/openpgp/armor"
	"github.com/ProtonMail/go-crypto/openpgp/clearsign"
	"github.com/ProtonMail/go-crypto/openpgp/packet"
)

// One server-wide OpenPGP key signs apt Release files and yum repomd.xml.
// It is generated on first use and stored (armored) in the settings table.

var pgpConfig = &packet.Config{DefaultHash: crypto.SHA256, RSABits: 3072}

func (a *App) key() (*openpgp.Entity, error) {
	a.keyMu.Lock()
	defer a.keyMu.Unlock()
	if a.signingKey != nil {
		return a.signingKey, nil
	}
	var armored string
	if a.getJSON("signing_key", &armored) {
		list, err := openpgp.ReadArmoredKeyRing(strings.NewReader(armored))
		if err == nil && len(list) > 0 {
			a.signingKey = list[0]
			return a.signingKey, nil
		}
	}
	e, err := openpgp.NewEntity("RepoMan", "repository signing key", "repoman@localhost", pgpConfig)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	w, err := armor.Encode(&buf, openpgp.PrivateKeyType, nil)
	if err != nil {
		return nil, err
	}
	if err := e.SerializePrivate(w, pgpConfig); err != nil {
		return nil, err
	}
	w.Close()
	if err := a.setJSON("signing_key", buf.String()); err != nil {
		return nil, err
	}
	a.signingKey = e
	return e, nil
}

func (a *App) publicKey() ([]byte, string, error) {
	e, err := a.key()
	if err != nil {
		return nil, "", err
	}
	var buf bytes.Buffer
	w, err := armor.Encode(&buf, openpgp.PublicKeyType, nil)
	if err != nil {
		return nil, "", err
	}
	if err := e.Serialize(w); err != nil {
		return nil, "", err
	}
	w.Close()
	return buf.Bytes(), strings.ToUpper(hex.EncodeToString(e.PrimaryKey.Fingerprint)), nil
}

// publicKeyHandler serves the armored public key (also at /repository/<apt|yum repo>/repository.key).
func (a *App) publicKeyHandler(w http.ResponseWriter, r *http.Request) {
	pub, _, err := a.publicKey()
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	w.Header().Set("Content-Type", "application/pgp-keys")
	w.Write(pub)
}

func (a *App) clearsign(data []byte) ([]byte, error) {
	e, err := a.key()
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	w, err := clearsign.Encode(&buf, e.PrivateKey, pgpConfig)
	if err != nil {
		return nil, err
	}
	w.Write(data)
	if err := w.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func (a *App) detachSign(data []byte) ([]byte, error) {
	e, err := a.key()
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	if err := openpgp.ArmoredDetachSign(&buf, e, bytes.NewReader(data), pgpConfig); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// SPDX-License-Identifier: BSD-2-Clause
// SPDX-FileCopyrightText: 2026 Aryan Ameri <github@aryan.ameri.coffee>

// Package manifesttest writes OCI image archives shaped like the carriers
// the build exports and skopeo copies from the registry, for tests of what
// reads them.
package manifesttest

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// The media types an archive's documents and layers carry.
const (
	MediaIndex    = "application/vnd.oci.image.index.v1+json"
	MediaManifest = "application/vnd.oci.image.manifest.v1+json"
	MediaConfig   = "application/vnd.oci.image.config.v1+json"
	// MediaLayerGzip is what BuildKit and skopeo write.
	MediaLayerGzip = "application/vnd.oci.image.layer.v1.tar+gzip"
	// MediaLayerTar is an uncompressed layer.
	MediaLayerTar = "application/vnd.oci.image.layer.v1.tar"
	// MediaLayerZstd is a zstd layer, written here as plain bytes.
	MediaLayerZstd = "application/vnd.oci.image.layer.v1.tar+zstd"
)

// CapabilityPAX is the PAX record that gives a file a capability, such as
// the cap_net_bind_service=ep upstream's own binary carries.
const CapabilityPAX = "SCHILY.xattr.security.capability"

// Entry is one extra file in an image's records layer.
type Entry struct {
	Name string
	Body []byte
}

// Image is one platform's carrier: /frankenphp in one layer, as the
// Containerfile's first COPY writes it, and /records/ in a second.
type Image struct {
	// Arch is the platform's architecture, such as arm64.
	Arch string
	// ConfigArch is the architecture the image's config names, when it
	// should disagree with Arch.
	ConfigArch string
	// Records are the files under /records/, by name.
	Records map[string][]byte
	// NoBinary leaves /frankenphp out.
	NoBinary bool
	// BinaryPAX are PAX records on /frankenphp, such as CapabilityPAX.
	BinaryPAX map[string]string
	// Extra are further entries in the records layer, such as a whiteout.
	Extra []Entry
	// LayerMediaType is both layers' media type; MediaLayerGzip if empty.
	LayerMediaType string
}

// Archive is a whole OCI archive.
type Archive struct {
	Images []Image
	// Flat lists each image's manifest in index.json itself, with no
	// platform, as skopeo writes a single image. Otherwise index.json names
	// one image index that holds every image and, for each, an attestation
	// manifest, as BuildKit and `skopeo copy --all` write them.
	Flat bool
}

// Write writes a as an OCI archive in a temporary directory and returns
// its path.
func Write(tb testing.TB, a Archive) string {
	tb.Helper()
	w := writer{tb: tb, blobs: map[string][]byte{}}
	var descs []descriptor
	for _, img := range a.Images {
		manifest := w.image(&img)
		d := w.blob(MediaManifest, manifest)
		if !a.Flat {
			d.Platform = &platform{Architecture: img.Arch, OS: "linux"}
		}
		descs = append(descs, d)
		if !a.Flat {
			descs = append(descs, w.attestation(d))
		}
	}
	top := descs
	if !a.Flat {
		nested := w.blob(MediaIndex, w.json(index{SchemaVersion: 2, MediaType: MediaIndex, Manifests: descs}))
		top = []descriptor{nested}
	}

	var out bytes.Buffer
	tw := tar.NewWriter(&out)
	w.file(tw, "oci-layout", []byte(`{"imageLayoutVersion":"1.0.0"}`))
	w.file(tw, "index.json", w.json(index{SchemaVersion: 2, MediaType: MediaIndex, Manifests: top}))
	for _, digest := range slices.Sorted(maps.Keys(w.blobs)) {
		w.file(tw, "blobs/sha256/"+digest[len("sha256:"):], w.blobs[digest])
	}
	if err := tw.Close(); err != nil {
		tb.Fatal(err)
	}
	path := filepath.Join(tb.TempDir(), "carrier.tar")
	if err := os.WriteFile(path, out.Bytes(), 0o600); err != nil {
		tb.Fatal(err)
	}
	return path
}

type platform struct {
	Architecture string `json:"architecture"`
	OS           string `json:"os"`
}

type descriptor struct {
	MediaType   string            `json:"mediaType"`
	Digest      string            `json:"digest"`
	Size        int               `json:"size"`
	Platform    *platform         `json:"platform,omitempty"`
	Annotations map[string]string `json:"annotations,omitempty"`
}

type index struct {
	SchemaVersion int          `json:"schemaVersion"`
	MediaType     string       `json:"mediaType"`
	Manifests     []descriptor `json:"manifests"`
}

type manifest struct {
	SchemaVersion int          `json:"schemaVersion"`
	MediaType     string       `json:"mediaType"`
	Config        descriptor   `json:"config"`
	Layers        []descriptor `json:"layers"`
}

type writer struct {
	tb    testing.TB
	blobs map[string][]byte
}

func (w *writer) json(v any) []byte {
	w.tb.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		w.tb.Fatal(err)
	}
	return data
}

func (w *writer) blob(mediaType string, data []byte) descriptor {
	sum := sha256.Sum256(data)
	digest := "sha256:" + hex.EncodeToString(sum[:])
	w.blobs[digest] = data
	return descriptor{MediaType: mediaType, Digest: digest, Size: len(data)}
}

// image writes img's layers and config and returns its manifest.
func (w *writer) image(img *Image) []byte {
	w.tb.Helper()
	mediaType := img.LayerMediaType
	if mediaType == "" {
		mediaType = MediaLayerGzip
	}
	var binary []tarEntry
	if !img.NoBinary {
		binary = append(binary, tarEntry{name: "frankenphp", mode: 0o755, body: []byte("\x7fELF not really"), pax: img.BinaryPAX})
	}
	records := make([]tarEntry, 0, 1+len(img.Records)+len(img.Extra))
	records = append(records, tarEntry{name: "records/", dir: true, mode: 0o755})
	for _, name := range slices.Sorted(maps.Keys(img.Records)) {
		records = append(records, tarEntry{name: "records/" + name, mode: 0o644, body: img.Records[name]})
	}
	for _, e := range img.Extra {
		records = append(records, tarEntry{name: e.Name, mode: 0o644, body: e.Body})
	}
	configArch := img.ConfigArch
	if configArch == "" {
		configArch = img.Arch
	}
	config := w.blob(MediaConfig, w.json(map[string]any{"architecture": configArch, "os": "linux"}))
	return w.json(manifest{
		SchemaVersion: 2,
		MediaType:     MediaManifest,
		Config:        config,
		Layers: []descriptor{
			w.blob(mediaType, w.layer(mediaType, binary)),
			w.blob(mediaType, w.layer(mediaType, records)),
		},
	})
}

// attestation writes an attestation manifest for the image subject
// describes, as BuildKit does for its provenance.
func (w *writer) attestation(subject descriptor) descriptor {
	statement := w.blob("application/vnd.in-toto+json", []byte(`{"_type":"https://in-toto.io/Statement/v1"}`))
	empty := w.blob("application/vnd.oci.empty.v1+json", []byte("{}"))
	d := w.blob(MediaManifest, w.json(manifest{SchemaVersion: 2, MediaType: MediaManifest, Config: empty, Layers: []descriptor{statement}}))
	d.Platform = &platform{Architecture: "unknown", OS: "unknown"}
	d.Annotations = map[string]string{
		"vnd.docker.reference.digest": subject.Digest,
		"vnd.docker.reference.type":   "attestation-manifest",
	}
	return d
}

type tarEntry struct {
	name string
	dir  bool
	mode int64
	body []byte
	pax  map[string]string
}

func (w *writer) layer(mediaType string, entries []tarEntry) []byte {
	w.tb.Helper()
	var raw bytes.Buffer
	tw := tar.NewWriter(&raw)
	for _, e := range entries {
		hdr := &tar.Header{Name: e.name, Mode: e.mode, Size: int64(len(e.body)), Typeflag: tar.TypeReg, PAXRecords: e.pax}
		if e.dir {
			hdr.Typeflag, hdr.Size = tar.TypeDir, 0
		}
		if len(e.pax) > 0 {
			hdr.Format = tar.FormatPAX
		}
		if err := tw.WriteHeader(hdr); err != nil {
			w.tb.Fatal(err)
		}
		if _, err := tw.Write(e.body); err != nil {
			w.tb.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		w.tb.Fatal(err)
	}
	if mediaType != MediaLayerGzip {
		return raw.Bytes()
	}
	var gz bytes.Buffer
	zw := gzip.NewWriter(&gz)
	if _, err := zw.Write(raw.Bytes()); err != nil {
		w.tb.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		w.tb.Fatal(err)
	}
	return gz.Bytes()
}

func (w *writer) file(tw *tar.Writer, name string, data []byte) {
	w.tb.Helper()
	if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o444, Size: int64(len(data)), Typeflag: tar.TypeReg}); err != nil {
		w.tb.Fatal(err)
	}
	if _, err := tw.Write(data); err != nil {
		w.tb.Fatal(err)
	}
}

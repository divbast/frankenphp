// SPDX-License-Identifier: BSD-2-Clause
// SPDX-FileCopyrightText: 2026 Aryan Ameri <github@aryan.ameri.coffee>

package manifest

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"path"
	"regexp"
	"slices"
	"strings"
)

// What FromOCIArchive can refuse, besides an archive or a record it cannot
// read. Each error it returns for one of these wraps it.
var (
	// ErrFileCapability is a /frankenphp that carries a file capability.
	// The kernel refuses to exec such a binary at all under --cap-drop ALL,
	// which every app runs under.
	ErrFileCapability = errors.New("carries a file capability, so no container running with --cap-drop ALL could exec it")
	// ErrNoImage is an archive with no image for the architecture asked
	// for, as when a published index lacks one.
	ErrNoImage = errors.New("no image for the architecture")
	// ErrArch is an image whose config or binary is for another
	// architecture than its platform names.
	ErrArch = errors.New("built for another architecture")
)

// The media types FromOCIArchive follows and reads.
const (
	mediaOCIIndex     = "application/vnd.oci.image.index.v1+json"
	mediaDockerList   = "application/vnd.docker.distribution.manifest.list.v2+json"
	mediaOCIManifest  = "application/vnd.oci.image.manifest.v1+json"
	mediaDockerImage  = "application/vnd.docker.distribution.manifest.v2+json"
	mediaOCILayerGzip = "application/vnd.oci.image.layer.v1.tar+gzip"
	mediaOCILayerTar  = "application/vnd.oci.image.layer.v1.tar"
	mediaDockerLayer  = "application/vnd.docker.image.rootfs.diff.tar.gzip"
)

// Limits on what is read into memory: every document an archive holds is
// a few kilobytes, as is every record.
const (
	maxDocument = 4 << 20
	maxRecord   = 8 << 20
	// maxDepth bounds how deep indexes may nest, which a loop of indexes
	// naming each other would otherwise make endless.
	maxDepth = 4
)

// capabilityPAX is the PAX record in which a tar carries a file's
// security.capability extended attribute.
const capabilityPAX = "SCHILY.xattr.security.capability"

var blobName = regexp.MustCompile(`^blobs/sha256/([0-9a-f]{64})$`)

// FromOCIArchive reads the manifest of the arch image in the OCI archive
// file archive: a carrier the build exported, or the published one skopeo
// copied, whose index may hold every architecture. It reads /records/ from
// the image's layers as they stand in the archive, without extracting them.
//
// It returns an error wrapping ErrFileCapability if the image's /frankenphp
// carries a file capability, ErrNoImage if the archive holds no linux image
// for arch, and ErrArch if the image's config or its binary's build
// records name another architecture.
func FromOCIArchive(archive, arch string) (Manifest, error) {
	m, err := fromOCIArchive(archive, arch)
	if err != nil {
		return Manifest{}, fmt.Errorf("%s: %w", archive, err)
	}
	return m, nil
}

func fromOCIArchive(archive, arch string) (Manifest, error) {
	f, err := os.Open(archive) //nolint:gosec // G304: the archive the caller asks to read
	if err != nil {
		return Manifest{}, err
	}
	// Read only, so a failed close loses nothing.
	defer func() { _ = f.Close() }()

	docs, err := readDocuments(f)
	if err != nil {
		return Manifest{}, err
	}
	layers, err := docs.layersFor(arch)
	if err != nil {
		return Manifest{}, err
	}
	if _, err = f.Seek(0, io.SeekStart); err != nil {
		return Manifest{}, err
	}
	img, err := readLayers(f, layers)
	if err != nil {
		return Manifest{}, err
	}

	switch binary := img.binary; {
	case binary == nil:
		return Manifest{}, errors.New("the image holds no /frankenphp")
	case binary.Typeflag != tar.TypeReg:
		return Manifest{}, errors.New("the image's /frankenphp is not a regular file")
	default:
		if _, ok := binary.PAXRecords[capabilityPAX]; ok {
			return Manifest{}, fmt.Errorf("/frankenphp %w", ErrFileCapability)
		}
	}

	m, err := parse(func(name string) ([]byte, error) {
		data, ok := img.records[name]
		if !ok {
			return nil, fmt.Errorf("/records/%s: %w", name, fs.ErrNotExist)
		}
		return data, nil
	})
	if err != nil {
		return Manifest{}, err
	}
	if m.Arch != arch {
		return Manifest{}, fmt.Errorf("the %s image's binary was built for GOARCH %s: %w", arch, m.Arch, ErrArch)
	}
	return m, nil
}

type platform struct {
	Architecture string `json:"architecture"`
	OS           string `json:"os"`
}

type descriptor struct {
	MediaType   string            `json:"mediaType"`
	Digest      string            `json:"digest"`
	Platform    *platform         `json:"platform"`
	Annotations map[string]string `json:"annotations"`
}

type document struct {
	MediaType string       `json:"mediaType"`
	Manifests []descriptor `json:"manifests"`
	Config    descriptor   `json:"config"`
	Layers    []descriptor `json:"layers"`
}

// documents are an archive's index.json and its blobs small enough to be
// documents, by digest.
type documents struct {
	index []byte
	blobs map[string][]byte
}

func readDocuments(r io.Reader) (documents, error) {
	docs := documents{blobs: map[string][]byte{}}
	tr := tar.NewReader(r)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return documents{}, fmt.Errorf("read the archive for its index.json: %w", err)
		}
		name := path.Clean(strings.TrimPrefix(hdr.Name, "/"))
		blob := blobName.FindStringSubmatch(name)
		if hdr.Typeflag != tar.TypeReg || hdr.Size > maxDocument || (name != "index.json" && blob == nil) {
			continue
		}
		data, err := io.ReadAll(tr)
		if err != nil {
			return documents{}, fmt.Errorf("read %s: %w", name, err)
		}
		if blob == nil {
			docs.index = data
		} else {
			docs.blobs["sha256:"+blob[1]] = data
		}
	}
	if docs.index == nil {
		return documents{}, errors.New("no index.json: not an OCI archive")
	}
	return docs, nil
}

func (d documents) decode(digest string) (document, error) {
	data, ok := d.blobs[digest]
	if !ok {
		return document{}, fmt.Errorf("the archive holds no document %s", digest)
	}
	var doc document
	if err := json.Unmarshal(data, &doc); err != nil {
		return document{}, fmt.Errorf("%s: %w", digest, err)
	}
	return doc, nil
}

// layersFor finds the one linux image for arch, following every index
// from index.json and passing over attestation manifests, and returns its
// layers in order.
func (d documents) layersFor(arch string) ([]descriptor, error) {
	var top document
	if err := json.Unmarshal(d.index, &top); err != nil {
		return nil, fmt.Errorf("index.json: %w", err)
	}
	found := map[string]bool{}
	if err := d.walk(top.Manifests, arch, 0, found); err != nil {
		return nil, err
	}
	switch len(found) {
	case 0:
		return nil, fmt.Errorf("linux/%s: %w", arch, ErrNoImage)
	case 1:
	default:
		return nil, fmt.Errorf("%d images for linux/%s, not one", len(found), arch)
	}
	image, err := d.decode(slices.Collect(maps.Keys(found))[0])
	if err != nil {
		return nil, err
	}
	config, ok := d.blobs[image.Config.Digest]
	if !ok {
		return nil, fmt.Errorf("the archive holds no config %s", image.Config.Digest)
	}
	var c platform
	if err := json.Unmarshal(config, &c); err != nil {
		return nil, fmt.Errorf("the config %s: %w", image.Config.Digest, err)
	}
	if c.Architecture != arch || c.OS != "linux" {
		return nil, fmt.Errorf("the linux/%s image's config is for %s/%s: %w", arch, c.OS, c.Architecture, ErrArch)
	}
	return image.Layers, nil
}

// walk adds to found the digest of every linux image for arch that descs,
// or the indexes among them, name.
func (d documents) walk(descs []descriptor, arch string, depth int, found map[string]bool) error {
	if depth > maxDepth {
		return fmt.Errorf("indexes nest more than %d deep", maxDepth)
	}
	for _, desc := range descs {
		switch desc.MediaType {
		case mediaOCIIndex, mediaDockerList:
			nested, err := d.decode(desc.Digest)
			if err != nil {
				return err
			}
			if err := d.walk(nested.Manifests, arch, depth+1, found); err != nil {
				return err
			}
		case mediaOCIManifest, mediaDockerImage:
			if desc.Annotations["vnd.docker.reference.type"] == "attestation-manifest" {
				continue
			}
			// A descriptor without a platform, as skopeo writes for one
			// image, leaves the image's config to say; layersFor checks it.
			if desc.Platform == nil || desc.Platform.Architecture == arch && desc.Platform.OS == "linux" {
				found[desc.Digest] = true
			}
		}
	}
	return nil
}

// tree is what an image's layers leave at /frankenphp and under /records/.
type tree struct {
	binary  *tar.Header
	records map[string][]byte
}

// readLayers reads layers from the archive r, and lays them over one
// another in order.
func readLayers(r io.Reader, layers []descriptor) (tree, error) {
	want := map[string]descriptor{}
	for _, l := range layers {
		want[l.Digest] = l
	}
	read := map[string]tree{}
	tr := tar.NewReader(r)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return tree{}, fmt.Errorf("read the archive for its layers: %w", err)
		}
		blob := blobName.FindStringSubmatch(path.Clean(strings.TrimPrefix(hdr.Name, "/")))
		if blob == nil {
			continue
		}
		layer, ok := want["sha256:"+blob[1]]
		if !ok {
			continue
		}
		t, err := readLayer(tr, layer.MediaType)
		if err != nil {
			return tree{}, fmt.Errorf("the layer %s: %w", layer.Digest, err)
		}
		read[layer.Digest] = t
	}

	merged := tree{records: map[string][]byte{}}
	for _, l := range layers {
		t, ok := read[l.Digest]
		if !ok {
			return tree{}, fmt.Errorf("the archive holds no layer %s", l.Digest)
		}
		if t.binary != nil {
			merged.binary = t.binary
		}
		maps.Copy(merged.records, t.records)
	}
	return merged, nil
}

func readLayer(r io.Reader, mediaType string) (tree, error) {
	switch mediaType {
	case mediaOCILayerGzip, mediaDockerLayer:
		zr, err := gzip.NewReader(r)
		if err != nil {
			return tree{}, err
		}
		// Read only, so a failed close loses nothing.
		defer func() { _ = zr.Close() }()
		r = zr
	case mediaOCILayerTar:
	default:
		return tree{}, fmt.Errorf("a %s layer, which this cannot read", mediaType)
	}

	t := tree{records: map[string][]byte{}}
	tr := tar.NewReader(r)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return t, nil
		}
		if err != nil {
			return tree{}, err
		}
		name := path.Clean("/" + hdr.Name)
		// A carrier is two COPYs onto scratch, which delete nothing.
		if strings.HasPrefix(path.Base(name), ".wh.") {
			return tree{}, fmt.Errorf("%s: a whiteout, which no carrier holds", name)
		}
		switch dir, file := path.Split(name); {
		case name == "/frankenphp":
			t.binary = hdr
		case dir == "/records/" && hdr.Typeflag == tar.TypeReg:
			data, err := io.ReadAll(io.LimitReader(tr, maxRecord+1))
			if err != nil {
				return tree{}, fmt.Errorf("%s: %w", name, err)
			}
			if len(data) > maxRecord {
				return tree{}, fmt.Errorf("%s: larger than %d bytes", name, maxRecord)
			}
			t.records[file] = data
		}
	}
}

package sops

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha512"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"filippo.io/age"
	"filippo.io/age/armor"
	"github.com/mikael/kubit/internal/yamlx"
	"go.yaml.in/yaml/v4"
)

const Version = "3.13.3"

const defaultUnencryptedSuffix = "_unencrypted"

var ErrMAC = errors.New("sops: MAC mismatch; the file was changed without its key")

type Rule struct {
	Age               []string `yaml:"-"`
	EncryptedRegex    string   `yaml:"encrypted_regex,omitempty"`
	UnencryptedRegex  string   `yaml:"unencrypted_regex,omitempty"`
	EncryptedSuffix   string   `yaml:"encrypted_suffix,omitempty"`
	UnencryptedSuffix string   `yaml:"unencrypted_suffix,omitempty"`
	MACOnlyEncrypted  bool     `yaml:"mac_only_encrypted,omitempty"`
}

type ageKey struct {
	Recipient string `yaml:"recipient"`
	Enc       string `yaml:"enc"`
}

type metadata struct {
	KeyGroups    []any    `yaml:"key_groups,omitempty"`
	Age          []ageKey `yaml:"age,omitempty"`
	LastModified string   `yaml:"lastmodified"`
	MAC          string   `yaml:"mac"`
	Rule         `yaml:",inline"`
	Version      string `yaml:"version"`
}

func (r Rule) encrypts(path []string) (bool, error) {
	enc := true
	if r.UnencryptedSuffix != "" && slices.ContainsFunc(path, func(k string) bool { return strings.HasSuffix(k, r.UnencryptedSuffix) }) {
		enc = false
	}
	if r.EncryptedSuffix != "" {
		enc = slices.ContainsFunc(path, func(k string) bool { return strings.HasSuffix(k, r.EncryptedSuffix) })
	}
	if r.EncryptedRegex != "" {
		re, err := regexp.Compile(r.EncryptedRegex)
		if err != nil {
			return false, err
		}
		enc = slices.ContainsFunc(path, re.MatchString)
	}
	if r.UnencryptedRegex != "" {
		re, err := regexp.Compile(r.UnencryptedRegex)
		if err != nil {
			return false, err
		}
		if slices.ContainsFunc(path, re.MatchString) {
			enc = false
		}
	}
	return enc, nil
}

func Encrypt(plain []byte, r Rule) ([]byte, error) {
	if len(r.Age) == 0 {
		return nil, errors.New("sops: no age recipients")
	}
	if r.UnencryptedSuffix == "" && r.EncryptedSuffix == "" && r.EncryptedRegex == "" && r.UnencryptedRegex == "" {
		r.UnencryptedSuffix = defaultUnencryptedSuffix
	}
	root, err := parseMapping(plain)
	if err != nil {
		return nil, err
	}
	if _, i := yamlx.Lookup(root, "sops"); i >= 0 {
		return nil, errors.New("sops: the file is already encrypted")
	}
	stripComments(root)
	dataKey := make([]byte, 32)
	if _, err := rand.Read(dataKey); err != nil {
		return nil, err
	}
	mac := sha512.New()
	err = walk(root, nil, func(n *yaml.Node, path []string) error {
		v, err := scalar(n)
		if err != nil {
			return err
		}
		enc, err := r.encrypts(path)
		if err != nil {
			return err
		}
		if v == nil {
			return nil
		}
		if !r.MACOnlyEncrypted || enc {
			mac.Write(macBytes(v))
		}
		if !enc {
			return nil
		}
		s, err := encryptValue(v, dataKey, strings.Join(path, ":")+":")
		if err != nil {
			return err
		}
		n.Value, n.Tag, n.Style = s, "!!str", 0
		return nil
	})
	if err != nil {
		return nil, err
	}
	md := metadata{LastModified: time.Now().UTC().Format(time.RFC3339), Rule: r, Version: Version}
	if md.MAC, err = encryptValue(fmt.Sprintf("%X", mac.Sum(nil)), dataKey, md.LastModified); err != nil {
		return nil, err
	}
	for _, rcp := range r.Age {
		k, err := wrapKey(dataKey, rcp)
		if err != nil {
			return nil, err
		}
		md.Age = append(md.Age, ageKey{Recipient: rcp, Enc: k})
	}
	var mdNode yaml.Node
	if err := mdNode.Encode(md); err != nil {
		return nil, err
	}
	root.Content = append(root.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "sops"}, &mdNode)
	return render(root)
}

func Decrypt(data []byte, ids []age.Identity) ([]byte, error) {
	root, md, err := split(data)
	if err != nil {
		return nil, err
	}
	if len(md.KeyGroups) > 0 {
		return nil, errors.New("sops: key groups are not supported")
	}
	dataKey, err := unwrapKey(md.Age, ids)
	if err != nil {
		return nil, err
	}
	mac := sha512.New()
	err = walk(root, nil, func(n *yaml.Node, path []string) error {
		n.HeadComment, n.LineComment, n.FootComment = dropEncrypted(n.HeadComment), dropEncrypted(n.LineComment), dropEncrypted(n.FootComment)
		enc, err := md.encrypts(path)
		if err != nil {
			return err
		}
		var v any
		if enc && n.Value != "" {
			if v, err = decryptValue(n.Value, dataKey, strings.Join(path, ":")+":"); err != nil {
				return fmt.Errorf("sops: %s: %w", strings.Join(path, "."), err)
			}
			setScalar(n, v)
		} else if v, err = scalar(n); err != nil {
			return err
		}
		if v != nil && (!md.MACOnlyEncrypted || enc) {
			mac.Write(macBytes(v))
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	stamp, err := time.Parse(time.RFC3339, md.LastModified)
	if err != nil {
		return nil, fmt.Errorf("sops: lastmodified: %w", err)
	}
	want, err := decryptValue(md.MAC, dataKey, stamp.UTC().Format(time.RFC3339))
	if err != nil {
		return nil, fmt.Errorf("sops: mac: %w", err)
	}
	if s, _ := want.(string); !strings.EqualFold(s, fmt.Sprintf("%X", mac.Sum(nil))) {
		return nil, ErrMAC
	}
	return render(root)
}

func Recipients(data []byte) ([]string, error) {
	_, md, err := split(data)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(md.Age))
	for _, k := range md.Age {
		out = append(out, k.Recipient)
	}
	return out, nil
}

func Encrypted(data []byte) bool {
	root, err := parseMapping(data)
	if err != nil {
		return false
	}
	_, i := yamlx.Lookup(root, "sops")
	return i >= 0
}

func split(data []byte) (*yaml.Node, *metadata, error) {
	root, err := parseMapping(data)
	if err != nil {
		return nil, nil, err
	}
	n, i := yamlx.Lookup(root, "sops")
	if i < 0 {
		return nil, nil, errors.New("sops: not an encrypted file (no sops metadata)")
	}
	var md metadata
	if err := n.Decode(&md); err != nil {
		return nil, nil, fmt.Errorf("sops: metadata: %w", err)
	}
	root.Content = slices.Delete(root.Content, i, i+2)
	return root, &md, nil
}

func parseMapping(data []byte) (*yaml.Node, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("sops: %w", err)
	}
	if doc.Kind == 0 {
		return &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}, nil
	}
	if doc.Kind != yaml.DocumentNode || len(doc.Content) != 1 || doc.Content[0].Kind != yaml.MappingNode {
		return nil, errors.New("sops: the document must be a YAML mapping")
	}
	return doc.Content[0], nil
}

func render(root *yaml.Node) ([]byte, error) {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(4)
	if err := enc.Encode(root); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func walk(n *yaml.Node, path []string, leaf func(*yaml.Node, []string) error) error {
	switch n.Kind {
	case yaml.MappingNode:
		for i := 0; i+1 < len(n.Content); i += 2 {
			if err := walk(n.Content[i+1], append(slices.Clip(path), n.Content[i].Value), leaf); err != nil {
				return err
			}
		}
	case yaml.SequenceNode:
		for _, c := range n.Content {
			if err := walk(c, path, leaf); err != nil {
				return err
			}
		}
	case yaml.ScalarNode:
		return leaf(n, path)
	case yaml.AliasNode:
		return errors.New("sops: YAML aliases are not supported")
	}
	return nil
}

func scalar(n *yaml.Node) (any, error) {
	var v any
	if err := n.Decode(&v); err != nil {
		return nil, err
	}
	switch t := v.(type) {
	case nil, string, int, float64, bool:
		return v, nil
	case uint64:
		return strconv.FormatUint(t, 10), nil
	case time.Time:
		return n.Value, nil
	}
	return n.Value, nil
}

func setScalar(n *yaml.Node, v any) {
	n.Style = 0
	switch t := v.(type) {
	case string:
		n.Value, n.Tag = t, "!!str"
		if strings.Contains(t, "\n") {
			n.Style = yaml.LiteralStyle
		}
	case int:
		n.Value, n.Tag = strconv.Itoa(t), "!!int"
	case float64:
		n.Value, n.Tag = strconv.FormatFloat(t, 'f', -1, 64), "!!float"
	case bool:
		n.Value, n.Tag = strconv.FormatBool(t), "!!bool"
	}
}

func macBytes(v any) []byte {
	switch t := v.(type) {
	case string:
		return []byte(t)
	case int:
		return []byte(strconv.Itoa(t))
	case float64:
		return []byte(strconv.FormatFloat(t, 'f', -1, 64))
	case bool:
		if t {
			return []byte("True")
		}
		return []byte("False")
	}
	return nil
}

var encPattern = regexp.MustCompile(`^ENC\[AES256_GCM,data:(.*),iv:(.+),tag:(.+),type:(.+)\]$`)

func encryptValue(v any, key []byte, ad string) (string, error) {
	var typ string
	var plain []byte
	switch t := v.(type) {
	case string:
		if t == "" {
			return "", nil
		}
		typ, plain = "str", []byte(t)
	case int:
		typ, plain = "int", []byte(strconv.Itoa(t))
	case float64:
		typ, plain = "float", []byte(strconv.FormatFloat(t, 'f', -1, 64))
	case bool:
		typ, plain = "bool", []byte(strconv.FormatBool(t))
	default:
		return "", fmt.Errorf("sops: cannot encrypt %T", v)
	}
	gcm, err := newGCM(key)
	if err != nil {
		return "", err
	}
	iv := make([]byte, 32)
	if _, err := rand.Read(iv); err != nil {
		return "", err
	}
	out := gcm.Seal(nil, iv, plain, []byte(ad))
	data, tag := out[:len(out)-gcm.Overhead()], out[len(out)-gcm.Overhead():]
	b64 := base64.StdEncoding.EncodeToString
	return fmt.Sprintf("ENC[AES256_GCM,data:%s,iv:%s,tag:%s,type:%s]", b64(data), b64(iv), b64(tag), typ), nil
}

func decryptValue(s string, key []byte, ad string) (any, error) {
	m := encPattern.FindStringSubmatch(s)
	if m == nil {
		return nil, errors.New("value is not encrypted")
	}
	var parts [3][]byte
	for i := range parts {
		b, err := base64.StdEncoding.DecodeString(m[i+1])
		if err != nil {
			return nil, err
		}
		parts[i] = b
	}
	gcm, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	plain, err := gcm.Open(nil, parts[1], append(parts[0], parts[2]...), []byte(ad))
	if err != nil {
		return nil, errors.New("cannot decrypt value")
	}
	switch m[4] {
	case "str", "bytes":
		return string(plain), nil
	case "int":
		return strconv.Atoi(string(plain))
	case "float":
		return strconv.ParseFloat(string(plain), 64)
	case "bool":
		return strconv.ParseBool(string(plain))
	}
	return nil, fmt.Errorf("unknown type %q", m[4])
}

func newGCM(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCMWithNonceSize(block, 32)
}

func wrapKey(dataKey []byte, recipient string) (string, error) {
	r, err := age.ParseX25519Recipient(recipient)
	if err != nil {
		return "", fmt.Errorf("sops: recipient %q: %w", recipient, err)
	}
	var buf bytes.Buffer
	aw := armor.NewWriter(&buf)
	w, err := age.Encrypt(aw, r)
	if err != nil {
		return "", err
	}
	if _, err := w.Write(dataKey); err != nil {
		return "", err
	}
	if err := w.Close(); err != nil {
		return "", err
	}
	if err := aw.Close(); err != nil {
		return "", err
	}
	return buf.String(), nil
}

func unwrapKey(keys []ageKey, ids []age.Identity) ([]byte, error) {
	if len(ids) == 0 {
		return nil, errors.New("sops: no age identity found; set SOPS_AGE_KEY_FILE or create " + DefaultKeyFile())
	}
	for _, k := range keys {
		r, err := age.Decrypt(armor.NewReader(strings.NewReader(k.Enc)), ids...)
		if err != nil {
			continue
		}
		key, err := io.ReadAll(r)
		if err == nil && len(key) == 32 {
			return key, nil
		}
	}
	rcps := make([]string, 0, len(keys))
	for _, k := range keys {
		rcps = append(rcps, k.Recipient)
	}
	return nil, fmt.Errorf("sops: none of your age keys opens this file (recipients %s)", strings.Join(rcps, ", "))
}

func dropEncrypted(c string) string {
	var keep []string
	for _, l := range strings.Split(c, "\n") {
		if !strings.HasPrefix(strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(l), "#")), "ENC[") {
			keep = append(keep, l)
		}
	}
	return strings.TrimSpace(strings.Join(keep, "\n"))
}

func stripComments(n *yaml.Node) {
	n.HeadComment, n.LineComment, n.FootComment = "", "", ""
	for _, c := range n.Content {
		stripComments(c)
	}
}

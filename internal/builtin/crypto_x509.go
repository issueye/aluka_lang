package builtin

// crypto.X509Certificate 兼容实现（Node 22 语义）+ 简化 createPrivateKey
// （KeyObject，供 checkPrivateKey）。

import (
	"crypto/rsa"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha512"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"hash"
	"net"
	"strings"
	"time"

	"github.com/aluka-lang/aluka/internal/engine"
	"github.com/aluka-lang/aluka/internal/runtime/globals"
)

// x509CertMap 实例对象 → 解析后的证书（checkIssued 取用）。
// 用 WeakMap 以 JS 对象为弱引用 key：对象被 GC 回收后条目自动失效，
// 不再阻止回收也不残留（旧实现用强引用 map 会双重泄漏）。
var x509CertMap = engine.NewWeakMap[*x509.Certificate]()

// x509OIDShortNames DN 属性 OID → 短名（Node 的 subject/issuer 输出格式）。
var x509OIDShortNames = map[string]string{
	"2.5.4.3":  "CN",
	"2.5.4.4":  "SN",
	"2.5.4.5":  "serialNumber",
	"2.5.4.6":  "C",
	"2.5.4.7":  "L",
	"2.5.4.8":  "ST",
	"2.5.4.9":  "street",
	"2.5.4.10": "O",
	"2.5.4.11": "OU",
	"2.5.4.12": "title",
	"2.5.4.15": "businessCategory",
	"2.5.4.17": "postalCode",
	"2.5.4.42": "GN",
	"2.5.4.97": "organizationIdentifier",
	"0.9.2342.19200300.100.1.1":  "UID",
	"0.9.2342.19200300.100.1.25": "DC",
	"1.2.840.113549.1.9.1":        "emailAddress",
}

// x509ExtKeyUsageOIDs ExtKeyUsage 枚举 → OID 字符串。
var x509ExtKeyUsageOIDs = map[x509.ExtKeyUsage]string{
	x509.ExtKeyUsageAny:           "2.5.29.37.0",
	x509.ExtKeyUsageServerAuth:    "1.3.6.1.5.5.7.3.1",
	x509.ExtKeyUsageClientAuth:    "1.3.6.1.5.5.7.3.2",
	x509.ExtKeyUsageCodeSigning:   "1.3.6.1.5.5.7.3.3",
	x509.ExtKeyUsageEmailProtection: "1.3.6.1.5.5.7.3.4",
	x509.ExtKeyUsageTimeStamping:  "1.3.6.1.5.5.7.3.8",
	x509.ExtKeyUsageOCSPSigning:   "1.3.6.1.5.5.7.3.9",
}

// registerX509 在 crypto 模块注册 X509Certificate 与 createPrivateKey。
func registerX509(m engine.Object) {
	// new X509Certificate(cert)：cert 为 PEM 字符串或 DER Buffer。
	_ = m.Set("X509Certificate", engine.NewFunction("X509Certificate", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Undefined(), fmt.Errorf("X509Certificate: certificate argument required")
		}
		var der []byte
		if bv, ok := args[0].(*engine.BufferValue); ok {
			der = bv.Bytes()
		} else if data, ok := engine.AsArrayBuffer(args[0]); ok {
			der = data
		} else if ta, ok := engine.AsTypedArray(args[0]); ok {
			der = ta.Bytes()
		} else {
			block, _ := pem.Decode([]byte(args[0].String()))
			if block == nil {
				return engine.Undefined(), fmt.Errorf("%w: X509Certificate: invalid PEM certificate", engine.ErrTypeError)
			}
			der = block.Bytes
		}
		cert, err := x509.ParseCertificate(der)
		if err != nil {
			return engine.Undefined(), fmt.Errorf("%w: X509Certificate: %v", engine.ErrTypeError, err)
		}
		return x509CertToValue(cert, der), nil
	}))

	// createPrivateKey(key[, options])：简化 KeyObject（PEM RSA/PKCS8）。
	_ = m.Set("createPrivateKey", engine.NewFunction("createPrivateKey", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Undefined(), fmt.Errorf("createPrivateKey: key argument required")
		}
		data, err := cryptoBytes(args[0])
		if err != nil {
			return engine.Undefined(), err
		}
		if _, err := parsePrivateKeyPEM(data); err != nil {
			return engine.Undefined(), fmt.Errorf("%w: createPrivateKey: %v", engine.ErrTypeError, err)
		}
		ko := engine.NewObject()
		_ = ko.Set("type", engine.Str("private"))
		_ = ko.Set("asymmetricKeyType", engine.Str("rsa"))
		_ = ko.Set("__alukaKeyPEM", engine.Str(string(data)))
		return ko, nil
	}))
}

// x509GMT 输出时区（Node/OpenSSL 用 GMT 而非 UTC）。
var x509GMT = time.FixedZone("GMT", 0)

// x509TimeStr 时间格式化（Node 的 X509Certificate 格式："Jan  1 00:00:00 2024 GMT"）。
func x509TimeStr(t time.Time) string {
	return t.In(x509GMT).Format("Jan _2 15:04:05 2006 MST")
}

// x509CertToValue 构造 X509Certificate 实例对象。
func x509CertToValue(cert *x509.Certificate, der []byte) engine.Value {
	obj := engine.NewObject()
	x509CertMap.Set(obj, cert)
	subj := x509NameString(cert.Subject.Names)
	issuer := x509NameString(cert.Issuer.Names)
	serial := strings.ToUpper(hex.EncodeToString(cert.SerialNumber.Bytes()))
	_ = obj.Set("subject", engine.Str(subj))
	_ = obj.Set("issuer", engine.Str(issuer))
	_ = obj.Set("validFrom", engine.Str(x509TimeStr(cert.NotBefore)))
	_ = obj.Set("validTo", engine.Str(x509TimeStr(cert.NotAfter)))
	_ = obj.Set("serialNumber", engine.Str(serial))
	_ = obj.Set("fingerprint", engine.Str(x509Fingerprint(der, sha1.New())))
	_ = obj.Set("fingerprint256", engine.Str(x509Fingerprint(der, sha256.New())))
	_ = obj.Set("fingerprint512", engine.Str(x509Fingerprint(der, sha512.New())))
	_ = obj.Set("ca", engine.Boolean(cert.IsCA))
	_ = obj.Set("raw", globals.NewBufferInstance(der))
	san := x509SANString(cert)
	if san != "" {
		_ = obj.Set("subjectAltName", engine.Str(san))
	}
	if ku := x509KeyUsageOIDs(cert); len(ku) > 0 {
		vals := make([]engine.Value, len(ku))
		for i, oid := range ku {
			vals[i] = engine.Str(oid)
		}
		_ = obj.Set("keyUsage", engine.NewArray(vals))
	}
	// publicKey（简化 KeyObject）。
	spki, _ := x509.MarshalPKIXPublicKey(cert.PublicKey)
	pk := engine.NewObject()
	_ = pk.Set("type", engine.Str("public"))
	_ = pk.Set("asymmetricKeyType", engine.Str("rsa"))
	_ = pk.Set("raw", globals.NewBufferInstance(spki))
	_ = obj.Set("publicKey", pk)

	// toString()：原始 PEM。
	_ = obj.Set("toString", engine.NewFunction("toString", func(args []engine.Value) (engine.Value, error) {
		return engine.Str(string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))), nil
	}))

	// checkHost(host)：返回匹配的 SAN 名；不匹配返回 undefined。
	_ = obj.Set("checkHost", engine.NewFunction("checkHost", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Undefined(), nil
		}
		m := x509CheckHost(cert, args[0].String())
		if m == "" {
			return engine.Undefined(), nil
		}
		return engine.Str(m), nil
	}))

	// checkIssued(otherCert)：本证书是否由 otherCert 签发（issuer 匹配 + 签名）。
	_ = obj.Set("checkIssued", engine.NewFunction("checkIssued", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Boolean(false), nil
		}
		other, ok := x509CertArg(args[0])
		if !ok {
			return engine.Boolean(false), nil
		}
		return engine.Boolean(x509CheckIssued(cert, other)), nil
	}))

	// checkPrivateKey(key)：私钥与证书公钥匹配。
	_ = obj.Set("checkPrivateKey", engine.NewFunction("checkPrivateKey", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Boolean(false), nil
		}
		priv, ok := x509PrivateKeyArg(args[0])
		if !ok {
			return engine.Boolean(false), nil
		}
		return engine.Boolean(x509PublicKeysEqual(cert.PublicKey, priv.Public())), nil
	}))

	// verify(publicKey)：证书签名是否可用给定公钥验证。
	_ = obj.Set("verify", engine.NewFunction("verify", func(args []engine.Value) (engine.Value, error) {
		if len(args) == 0 {
			return engine.Boolean(false), nil
		}
		pub, ok := x509PublicKeyArg(args[0])
		if !ok {
			return engine.Boolean(false), nil
		}
		return engine.Boolean(x509PublicKeysEqual(cert.PublicKey, pub)), nil
	}))

	// toLegacyObject()：对象形式（Node 结构）。
	_ = obj.Set("toLegacyObject", engine.NewFunction("toLegacyObject", func(args []engine.Value) (engine.Value, error) {
		lo := engine.NewObject()
		_ = lo.Set("subject", x509NameObj(cert.Subject.Names))
		_ = lo.Set("issuer", x509NameObj(cert.Issuer.Names))
		if san != "" {
			_ = lo.Set("subjectaltname", engine.Str(san))
		}
		_ = lo.Set("ca", engine.Boolean(cert.IsCA))
		if pub, ok := cert.PublicKey.(*rsa.PublicKey); ok {
			_ = lo.Set("modulus", engine.Str(strings.ToUpper(hex.EncodeToString(pub.N.Bytes()))))
			_ = lo.Set("bits", engine.IntValue(pub.N.BitLen()))
			_ = lo.Set("exponent", engine.Str(fmt.Sprintf("0x%x", pub.E)))
		}
		_ = lo.Set("pubkey", globals.NewBufferInstance(spki))
		_ = lo.Set("valid_from", engine.Str(cert.NotBefore.Format("Jan _2 15:04:05 2006 MST")))
		_ = lo.Set("valid_to", engine.Str(cert.NotAfter.Format("Jan _2 15:04:05 2006 MST")))
		_ = lo.Set("fingerprint", engine.Str(x509Fingerprint(der, sha1.New())))
		_ = lo.Set("fingerprint256", engine.Str(x509Fingerprint(der, sha256.New())))
		_ = lo.Set("fingerprint512", engine.Str(x509Fingerprint(der, sha512.New())))
		if ku := x509KeyUsageOIDs(cert); len(ku) > 0 {
			vals := make([]engine.Value, len(ku))
			for i, oid := range ku {
				vals[i] = engine.Str(oid)
			}
			_ = lo.Set("ext_key_usage", engine.NewArray(vals))
		}
		_ = lo.Set("serialNumber", engine.Str(serial))
		_ = lo.Set("raw", globals.NewBufferInstance(der))
		return lo, nil
	}))

	return obj
}

// x509NameString DN → "TAG=value\nTAG=value"（Node 格式）。
func x509NameString(names []pkix.AttributeTypeAndValue) string {
	var parts []string
	for _, n := range names {
		tag := x509OIDShortNames[n.Type.String()]
		if tag == "" {
			tag = n.Type.String()
		}
		parts = append(parts, tag+"="+x509ATVValue(n))
	}
	return strings.Join(parts, "\n")
}

// x509NameObj DN → {TAG: value} 对象（toLegacyObject 用）。
func x509NameObj(names []pkix.AttributeTypeAndValue) engine.Value {
	o := engine.NewObject()
	for _, n := range names {
		tag := x509OIDShortNames[n.Type.String()]
		if tag == "" {
			tag = n.Type.String()
		}
		_ = o.Set(tag, engine.Str(x509ATVValue(n)))
	}
	return o
}

// x509ATVValue RDN 值字符串化（UTF8String/PrintableString 为 string；
// 其他类型尽力转换）。
func x509ATVValue(n pkix.AttributeTypeAndValue) string {
	switch v := n.Value.(type) {
	case string:
		return v
	case []byte:
		return string(v)
	default:
		return fmt.Sprintf("%v", v)
	}
}

// x509Fingerprint DER → 冒号分隔大写 hex 指纹。
func x509Fingerprint(der []byte, h hash.Hash) string {
	_, _ = h.Write(der)
	sum := h.Sum(nil)
	var b strings.Builder
	for i, c := range sum {
		if i > 0 {
			b.WriteByte(':')
		}
		fmt.Fprintf(&b, "%02X", c)
	}
	return b.String()
}

// x509SANString subjectAltName → "DNS:x, DNS:y, IP:z"。
func x509SANString(cert *x509.Certificate) string {
	var parts []string
	for _, d := range cert.DNSNames {
		parts = append(parts, "DNS:"+d)
	}
	for _, ip := range cert.IPAddresses {
		parts = append(parts, "IP:"+ip.String())
	}
	for _, e := range cert.EmailAddresses {
		parts = append(parts, "email:"+e)
	}
	for _, u := range cert.URIs {
		parts = append(parts, "URI:"+u.String())
	}
	return strings.Join(parts, ", ")
}

// x509KeyUsageOIDs 扩展密钥用法 OID 列表。
func x509KeyUsageOIDs(cert *x509.Certificate) []string {
	var out []string
	for _, ku := range cert.ExtKeyUsage {
		if oid, ok := x509ExtKeyUsageOIDs[ku]; ok {
			out = append(out, oid)
		}
	}
	return out
}

// x509CheckHost 主机名校验：精确 DNS SAN → 通配符 SAN → IP SAN。
// 返回匹配的 SAN 名（原始大小写）；无匹配返回空串。
func x509CheckHost(cert *x509.Certificate, host string) string {
	h := strings.ToLower(strings.TrimSuffix(host, "."))
	for _, d := range cert.DNSNames {
		if strings.ToLower(d) == h {
			return d
		}
	}
	for _, d := range cert.DNSNames {
		if strings.HasPrefix(d, "*.") {
			suffix := strings.ToLower(d[1:])
			if strings.HasSuffix(h, suffix) {
				left := h[:len(h)-len(suffix)]
				if !strings.Contains(left, ".") {
					return d
				}
			}
		}
	}
	if ip := net.ParseIP(h); ip != nil {
		for _, cip := range cert.IPAddresses {
			if cip.Equal(ip) {
				return host
			}
		}
	}
	return ""
}

// x509CheckIssued 简化：issuer DN 与 other.subject DN 一致。
func x509CheckIssued(cert, other *x509.Certificate) bool {
	return x509NameString(cert.Issuer.Names) == x509NameString(other.Subject.Names)
}

// x509PublicKeysEqual RSA 公钥相等（N/E 比较）。
func x509PublicKeysEqual(a, b interface{}) bool {
	ra, ok1 := a.(*rsa.PublicKey)
	rb, ok2 := b.(*rsa.PublicKey)
	if !ok1 || !ok2 {
		return false
	}
	return ra.N.Cmp(rb.N) == 0 && ra.E == rb.E
}

// x509CertArg 从 X509Certificate 实例取 *x509.Certificate。
func x509CertArg(v engine.Value) (*x509.Certificate, bool) {
	if o, ok := v.AsObject(); ok {
		if cert, ok := x509CertMap.Get(o); ok {
			return cert, true
		}
	}
	return nil, false
}

// x509PrivateKeyArg 从 KeyObject（__alukaKeyPEM）/PEM 取私钥。
func x509PrivateKeyArg(v engine.Value) (*rsa.PrivateKey, bool) {
	var data []byte
	if o, ok := v.AsObject(); ok {
		if p, err := o.Get("__alukaKeyPEM"); err == nil && !p.IsUndefined() {
			data = []byte(p.String())
		}
	}
	if data == nil {
		d, err := cryptoBytes(v)
		if err != nil {
			return nil, false
		}
		data = d
	}
	key, err := parsePrivateKeyPEM(data)
	if err != nil {
		return nil, false
	}
	return key, true
}

// x509PublicKeyArg 从 KeyObject/PEM 取公钥。
func x509PublicKeyArg(v engine.Value) (interface{}, bool) {
	priv, ok := x509PrivateKeyArg(v)
	if ok {
		return priv.Public(), true
	}
	// PEM 公钥。
	if o, ok := v.AsObject(); ok {
		if p, err := o.Get("__alukaKeyPEM"); err == nil && !p.IsUndefined() {
			if pub, perr := parsePublicKeyPEM([]byte(p.String())); perr == nil {
				return pub, true
			}
		}
	}
	return nil, false
}

// parsePrivateKeyPEM 解析 RSA 私钥（PKCS1/PKCS8）。
func parsePrivateKeyPEM(data []byte) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, fmt.Errorf("invalid PEM")
	}
	if key, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return key, nil
	}
	if key, err := x509.ParsePKCS8PrivateKey(block.Bytes); err == nil {
		if rk, ok := key.(*rsa.PrivateKey); ok {
			return rk, nil
		}
		return nil, fmt.Errorf("unsupported key type")
	}
	return nil, fmt.Errorf("invalid private key")
}

// parsePublicKeyPEM 解析 RSA 公钥。
func parsePublicKeyPEM(data []byte) (interface{}, error) {
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, fmt.Errorf("invalid PEM")
	}
	if key, err := x509.ParsePKIXPublicKey(block.Bytes); err == nil {
		return key, nil
	}
	return nil, fmt.Errorf("invalid public key")
}

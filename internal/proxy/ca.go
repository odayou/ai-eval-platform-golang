package proxy

// ca.go — MITM CA 证书管理。
//
// 为 HTTPS MITM 代理提供 CA 证书生成和管理功能。
// 自签 CA 用于动态签发域名证书，实现对 TLS 流量的明文捕获。

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"log"
	"math/big"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// CertPaths 记录 CA 证书和密钥的文件路径。
var CertPaths = struct {
	CACert string
	CAKey  string
	CertDir string
}{
	CACert:  filepath.Join(os.TempDir(), "ai-eval-mitm-ca.crt"),
	CAKey:   filepath.Join(os.TempDir(), "ai-eval-mitm-ca.key"),
	CertDir: filepath.Join(os.TempDir(), "ai-eval-mitm-certs"),
}

// HTTPHandler 是 HTTP 请求处理函数类型。
type HTTPHandler func(w http.ResponseWriter, r *http.Request)

// CAExists 检查 CA 证书和密钥是否已存在。
func CAExists() bool {
	_, certErr := os.Stat(CertPaths.CACert)
	_, keyErr := os.Stat(CertPaths.CAKey)
	return certErr == nil && keyErr == nil
}

// EnsureCA 确保 CA 证书存在，不存在则生成。
func EnsureCA() error {
	if CAExists() {
		return nil
	}
	return generateCA()
}

// generateCA 生成自签 CA 证书和密钥。
func generateCA() error {
	// 生成 RSA 私钥
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return fmt.Errorf("生成 CA 私钥失败: %w", err)
	}

	// 生成序列号
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return fmt.Errorf("生成序列号失败: %w", err)
	}

	// CA 证书模板
	template := &x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			CommonName:   "AI Eval Platform MITM CA",
			Organization: []string{"AI Eval Platform"},
		},
		NotBefore:             time.Now().Add(-24 * time.Hour),
		NotAfter:              time.Now().Add(10 * 365 * 24 * time.Hour), // 10 年
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLen:            0,
	}

	// 自签名
	certDER, err := x509.CreateCertificate(rand.Reader, template, template, &priv.PublicKey, priv)
	if err != nil {
		return fmt.Errorf("创建 CA 证书失败: %w", err)
	}

	// 确保目录存在
	os.MkdirAll(filepath.Dir(CertPaths.CACert), 0700)

	// 写入证书
	certFile, err := os.Create(CertPaths.CACert)
	if err != nil {
		return fmt.Errorf("创建证书文件失败: %w", err)
	}
	defer certFile.Close()
	pem.Encode(certFile, &pem.Block{Type: "CERTIFICATE", Bytes: certDER})

	// 写入密钥
	keyFile, err := os.Create(CertPaths.CAKey)
	if err != nil {
		return fmt.Errorf("创建密钥文件失败: %w", err)
	}
	defer keyFile.Close()
	pem.Encode(keyFile, &pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(priv),
	})

	// 创建证书目录
	os.MkdirAll(CertPaths.CertDir, 0700)

	log.Printf("[ca] CA 证书已生成: %s", CertPaths.CACert)
	log.Printf("[ca] CA 密钥已生成: %s", CertPaths.CAKey)
	return nil
}

// CreateTLSConfig 创建用于 MITM 的 TLS 配置。
// 配置了动态证书签发回调，在 TLS 握手时根据 SNI 动态签发证书。
func CreateTLSConfig() (*tls.Config, error) {
	if !CAExists() {
		if err := EnsureCA(); err != nil {
			return nil, err
		}
	}

	// 读取 CA 证书和密钥
	caCertPEM, err := os.ReadFile(CertPaths.CACert)
	if err != nil {
		return nil, fmt.Errorf("读取 CA 证书失败: %w", err)
	}
	caKeyPEM, err := os.ReadFile(CertPaths.CAKey)
	if err != nil {
		return nil, fmt.Errorf("读取 CA 密钥失败: %w", err)
	}

	// 解析 CA
	caCertBlock, _ := pem.Decode(caCertPEM)
	if caCertBlock == nil {
		return nil, fmt.Errorf("解析 CA 证书 PEM 失败")
	}
	caCert, err := x509.ParseCertificate(caCertBlock.Bytes)
	if err != nil {
		return nil, fmt.Errorf("解析 CA 证书失败: %w", err)
	}

	caKeyBlock, _ := pem.Decode(caKeyPEM)
	if caKeyBlock == nil {
		return nil, fmt.Errorf("解析 CA 密钥 PEM 失败")
	}
	caKey, err := x509.ParsePKCS1PrivateKey(caKeyBlock.Bytes)
	if err != nil {
		return nil, fmt.Errorf("解析 CA 密钥失败: %w", err)
	}

	// 证书缓存(避免同一域名重复签发)
	certCache := make(map[string]*tls.Certificate)

	tlsConfig := &tls.Config{
		// GetCertificate 在 TLS 握手时根据 ClientHello 的 SNI 动态签发证书
		GetCertificate: func(clientHello *tls.ClientHelloInfo) (*tls.Certificate, error) {
			domain := clientHello.ServerName
			if domain == "" {
				domain = "localhost"
			}

			// 检查缓存
			if cert, ok := certCache[domain]; ok {
				return cert, nil
			}

			// 动态签发证书
			cert, err := signCert(caCert, caKey, domain)
			if err != nil {
				return nil, fmt.Errorf("签发出生证失败(%s): %w", domain, err)
			}

			certCache[domain] = cert
			log.Printf("[ca] 动态签发证书: %s", domain)
			return cert, nil
		},
	}

	return tlsConfig, nil
}

// signCert 用 CA 为指定域名签发证书。
func signCert(caCert *x509.Certificate, caKey *rsa.PrivateKey, domain string) (*tls.Certificate, error) {
	// 生成服务器密钥
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, err
	}

	// 生成序列号
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, err
	}

	// 计算 SubjectKeyId(subjectPublicKey 的 SHA-256 前 8 位)
	pubDER, err := x509.MarshalPKIXPublicKey(&priv.PublicKey)
	if err != nil {
		return nil, err
	}
	hash := sha256.Sum256(pubDER)

	// 证书模板
	template := &x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			CommonName:   domain,
			Organization: []string{"AI Eval Platform MITM"},
		},
		NotBefore:             time.Now().Add(-1 * time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour), // 24 小时有效期
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  false,
		SubjectKeyId:          hash[:8],
	}

	// 添加 SAN(Subject Alternative Name)
	if strings.Contains(domain, ".") {
		template.DNSNames = []string{domain}
	} else {
		template.DNSNames = []string{domain, "localhost"}
	}

	// CA 签名
	certDER, err := x509.CreateCertificate(rand.Reader, template, caCert, &priv.PublicKey, caKey)
	if err != nil {
		return nil, fmt.Errorf("CA 签发失败: %w", err)
	}

	cert := &tls.Certificate{
		Certificate: [][]byte{certDER, caCert.Raw},
		PrivateKey:  priv,
	}

	return cert, nil
}

// GetCAInstallInstructions 返回 CA 证书安装指引。
func GetCAInstallInstructions() string {
	var instructions string
	switch runtime.GOOS {
	case "windows":
		instructions = fmt.Sprintf(`Windows 系统安装 CA 证书:

1. 双击或运行: certutil -addstore Root %s
2. 或: 打开 mmc → 添加/删除管理单元 → 证书 → 受信任的根证书颁发机构 → 导入
3. 或: 设置 → 隐私和安全性 → 安全 → 管理证书 → 受信任的根证书颁发机构 → 导入

文件路径: %s`, CertPaths.CACert, CertPaths.CACert)
	case "darwin":
		instructions = fmt.Sprintf(`macOS 安装 CA 证书:

sudo security add-trusted-cert -d -r trustRoot -k /Library/Keychains/System.keychain %s

文件路径: %s`, CertPaths.CACert, CertPaths.CACert)
	default:
		instructions = fmt.Sprintf(`Linux 安装 CA 证书:

# Debian/Ubuntu
sudo cp %s /usr/local/share/ca-certificates/ai-eval-mitm-ca.crt
sudo update-ca-certificates

# Fedora/RHEL
sudo cp %s /etc/pki/ca-trust/source/anchors/
sudo update-ca-trust

文件路径: %s`, CertPaths.CACert, CertPaths.CACert, CertPaths.CACert)
	}
	return instructions
}

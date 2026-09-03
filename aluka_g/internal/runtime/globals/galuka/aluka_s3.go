package galuka

// Aluka.S3 — S3 兼容对象存储客户端（Phase 4 WBS 4.20）。
//
// 自研 AWS Signature V4 签名（crypto/hmac + crypto/sha256，避免引入
// aws-sdk-go-v2 巨型依赖树），path-style 寻址，兼容 AWS S3 / MinIO /
// localstack 等 S3 兼容服务。
//
//	Aluka.S3()                                  // 从 AWS_* env 读取
//	Aluka.S3({ accessKeyId, secretAccessKey, region, endpoint, bucket })
//
// 环境变量：AWS_ACCESS_KEY_ID / AWS_SECRET_ACCESS_KEY / AWS_REGION /
// AWS_ENDPOINT_URL_S3 / S3_BUCKET。
//
// 客户端方法（均返回 Promise）：
//	get(key)       → { size, contentType, text(), json(), arrayBuffer() }
//	put(key, body) → "OK"（body 为字符串/Buffer）
//	delete(key)    → "OK"
//	list(prefix?)  → [{ key, size, lastModified, etag }]
//	exists(key)    → boolean（HEAD 200 / 404）

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/aluka-lang/aluka/internal/engine"
	"github.com/aluka-lang/aluka/internal/runtime/globals/gbase"
)

const (
	s3Service         = "s3"
	s3DefaultRegion   = "us-east-1"
	s3DefaultEndpoint = "https://s3.amazonaws.com"
)

// alukaRegisterS3 注册 Aluka.S3。
func RegisterS3(ctx engine.Context, aluka engine.Value) {
	ao, _ := aluka.AsObject()
	_ = ao.Set("S3", engine.NewFunction("S3", func(args []engine.Value) (engine.Value, error) {
		cfg := s3ConfigFromArgs(args)
		return buildS3Client(ctx, cfg), nil
	}))
}

// s3Config 描述 S3 客户端配置。
type s3Config struct {
	endpoint  string
	region    string
	accessKey string
	secretKey string
	bucket    string
}

// s3ConfigFromArgs 从参数对象与环境变量解析配置。
func s3ConfigFromArgs(args []engine.Value) s3Config {
	cfg := s3Config{
		endpoint:  os.Getenv("AWS_ENDPOINT_URL_S3"),
		region:    os.Getenv("AWS_REGION"),
		accessKey: os.Getenv("AWS_ACCESS_KEY_ID"),
		secretKey: os.Getenv("AWS_SECRET_ACCESS_KEY"),
		bucket:    os.Getenv("S3_BUCKET"),
	}
	if len(args) > 0 {
		if ao, ok := args[0].AsObject(); ok {
			get := func(key string) string {
				v, _ := ao.Get(key)
				if v.IsUndefined() {
					return ""
				}
				return v.String()
			}
			if v := get("endpoint"); v != "" {
				cfg.endpoint = v
			}
			if v := get("region"); v != "" {
				cfg.region = v
			}
			if v := get("accessKeyId"); v != "" {
				cfg.accessKey = v
			}
			if v := get("secretAccessKey"); v != "" {
				cfg.secretKey = v
			}
			if v := get("bucket"); v != "" {
				cfg.bucket = v
			}
		}
	}
	if cfg.region == "" {
		cfg.region = s3DefaultRegion
	}
	if cfg.endpoint == "" {
		cfg.endpoint = s3DefaultEndpoint
	}
	return cfg
}

// buildS3Client 构造 S3 客户端对象。
func buildS3Client(ctx engine.Context, cfg s3Config) engine.Value {
	obj := engine.NewObject()
	method := func(name string) engine.Value {
		return engine.NewFunction(name, func(args []engine.Value) (engine.Value, error) {
			executor := engine.NewFunction("executor", func(ea []engine.Value) (engine.Value, error) {
				if len(ea) == 0 {
					return engine.Undefined(), nil
				}
				resolve := ea[0]
				var reject engine.Value
				if len(ea) > 1 {
					reject = ea[1]
				}
				release := ctx.AddRef()
				go func() {
					result, err := doS3(ctx, cfg, name, args)
					ctx.PostTask(func() {
						defer release()
						if err != nil {
							if reject != nil {
								gbase.CallReject(reject, err.Error())
							}
							return
						}
						gbase.CallResolve(resolve, result)
					})
				}()
				return engine.Undefined(), nil
			})
			return gbase.NewPromise(ctx, executor)
		})
	}
	for _, name := range []string{"get", "put", "delete", "list", "exists"} {
		_ = obj.Set(name, method(name))
	}
	return obj
}

// doS3 执行 S3 操作（在 Go goroutine 中调用）。
func doS3(ctx engine.Context, cfg s3Config, method string, args []engine.Value) (engine.Value, error) {
	if cfg.bucket == "" {
		return engine.Undefined(), fmt.Errorf("Aluka.S3: bucket not configured (set S3_BUCKET or pass {bucket})")
	}
	if cfg.accessKey == "" || cfg.secretKey == "" {
		return engine.Undefined(), fmt.Errorf("Aluka.S3: credentials not configured (set AWS_ACCESS_KEY_ID / AWS_SECRET_ACCESS_KEY)")
	}
	switch method {
	case "get":
		key := s3Str(args, 0)
		status, body, header, err := s3Request(cfg, http.MethodGet, key, nil, nil)
		if err != nil {
			return engine.Undefined(), err
		}
		if status == http.StatusNotFound {
			return engine.Null(), nil
		}
		if status != http.StatusOK {
			return engine.Undefined(), fmt.Errorf("Aluka.S3: GET %s: status %d", key, status)
		}
		return buildS3FileValue(ctx, body, header.Get("Content-Type"), header.Get("Content-Length")), nil
	case "put":
		key := s3Str(args, 0)
		body := gbase.ArgBytes(args, 1)
		status, _, _, err := s3Request(cfg, http.MethodPut, key, nil, body)
		if err != nil {
			return engine.Undefined(), err
		}
		if status != http.StatusOK {
			return engine.Undefined(), fmt.Errorf("Aluka.S3: PUT %s: status %d", key, status)
		}
		return engine.Str("OK"), nil
	case "delete":
		key := s3Str(args, 0)
		status, _, _, err := s3Request(cfg, http.MethodDelete, key, nil, nil)
		if err != nil {
			return engine.Undefined(), err
		}
		if status != http.StatusNoContent && status != http.StatusOK {
			return engine.Undefined(), fmt.Errorf("Aluka.S3: DELETE %s: status %d", key, status)
		}
		return engine.Str("OK"), nil
	case "list":
		prefix := s3Str(args, 0)
		status, body, _, err := s3Request(cfg, http.MethodGet, "", url.Values{
			"list-type": {"2"},
			"prefix":    {prefix},
		}, nil)
		if err != nil {
			return engine.Undefined(), err
		}
		if status != http.StatusOK {
			return engine.Undefined(), fmt.Errorf("Aluka.S3: LIST: status %d", status)
		}
		return parseS3List(body), nil
	case "exists":
		key := s3Str(args, 0)
		status, _, _, err := s3Request(cfg, http.MethodHead, key, nil, nil)
		if err != nil {
			return engine.Undefined(), err
		}
		return engine.Boolean(status == http.StatusOK), nil
	}
	return engine.Undefined(), fmt.Errorf("Aluka.S3: unknown method %s", method)
}

// s3Request 构造签名请求并发送。query 为 nil 时无查询参数。
// 返回 (状态码, 响应体, 响应头, 错误)。
func s3Request(cfg s3Config, method, key string, query url.Values, body []byte) (int, []byte, http.Header, error) {
	u := strings.TrimSuffix(cfg.endpoint, "/") + "/" + url.PathEscape(cfg.bucket)
	if key != "" {
		u += "/" + awsPathEscape(key)
	}
	if len(query) > 0 {
		u += "?" + canonicalQuery(query)
	}
	payloadHash := sha256Hex(body)
	req, err := http.NewRequest(method, u, strings.NewReader(string(body)))
	if err != nil {
		return 0, nil, nil, err
	}
	if method == http.MethodPut {
		req.Header.Set("Content-Type", "application/octet-stream")
	}
	signS3Request(req, payloadHash, cfg)
	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return 0, nil, nil, fmt.Errorf("Aluka.S3: request: %w", err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, respBody, resp.Header, nil
}

// signS3Request 附加 AWS Signature V4 签名头。
func signS3Request(req *http.Request, payloadHash string, cfg s3Config) {
	now := time.Now().UTC()
	amzDate := now.Format("20060102T150405Z")
	dateStamp := now.Format("20060102")
	req.Header.Set("X-Amz-Date", amzDate)
	req.Header.Set("X-Amz-Content-Sha256", payloadHash)

	canonicalURI := req.URL.EscapedPath()
	if canonicalURI == "" {
		canonicalURI = "/"
	}
	canonicalHeaders := "host:" + req.Host + "\n" +
		"x-amz-content-sha256:" + payloadHash + "\n" +
		"x-amz-date:" + amzDate + "\n"
	signedHeaders := "host;x-amz-content-sha256;x-amz-date"
	canonicalRequest := req.Method + "\n" + canonicalURI + "\n" + req.URL.RawQuery + "\n" +
		canonicalHeaders + "\n" + signedHeaders + "\n" + payloadHash

	scope := dateStamp + "/" + cfg.region + "/" + s3Service + "/aws4_request"
	stringToSign := "AWS4-HMAC-SHA256\n" + amzDate + "\n" + scope + "\n" + sha256Hex([]byte(canonicalRequest))

	signingKey := s3SigningKey(cfg.secretKey, dateStamp, cfg.region, s3Service)
	signature := hex.EncodeToString(hmacSHA256(signingKey, stringToSign))
	req.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential="+cfg.accessKey+"/"+scope+
		", SignedHeaders="+signedHeaders+", Signature="+signature)
}

// s3SigningKey 计算签名密钥链。
func s3SigningKey(secretKey, dateStamp, region, service string) []byte {
	dateKey := hmacSHA256([]byte("AWS4"+secretKey), dateStamp)
	regionKey := hmacSHA256(dateKey, region)
	serviceKey := hmacSHA256(regionKey, service)
	return hmacSHA256(serviceKey, "aws4_request")
}

func hmacSHA256(key []byte, data string) []byte {
	h := hmac.New(sha256.New, key)
	_, _ = h.Write([]byte(data))
	return h.Sum(nil)
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// awsPathEscape 转义 S3 key 的路径段（保留 / 分隔符）。
func awsPathEscape(key string) string {
	parts := strings.Split(key, "/")
	for i, p := range parts {
		parts[i] = url.PathEscape(p)
	}
	return strings.Join(parts, "/")
}

// canonicalQuery 按 AWS 规则序列化查询参数（键值均排序，空格编码为 %20）。
func canonicalQuery(params url.Values) string {
	keys := make([]string, 0, len(params))
	for k := range params {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var parts []string
	for _, k := range keys {
		vals := params[k]
		sort.Strings(vals)
		for _, v := range vals {
			parts = append(parts, awsQueryEscape(k)+"="+awsQueryEscape(v))
		}
	}
	return strings.Join(parts, "&")
}

// awsQueryEscape 按 AWS 规范转义查询参数（%20 而非 +）。
func awsQueryEscape(s string) string {
	return strings.ReplaceAll(url.QueryEscape(s), "+", "%20")
}

// buildS3FileValue 构造 GET 返回的文件对象。
func buildS3FileValue(ctx engine.Context, body []byte, contentType, contentLength string) engine.Value {
	obj := engine.NewObject()
	if contentType != "" {
		_ = obj.Set("contentType", engine.Str(contentType))
	}
	size := len(body)
	if contentLength != "" {
		if n, err := strconv.ParseInt(contentLength, 10, 64); err == nil {
			size = int(n)
		}
	}
	_ = obj.Set("size", engine.Number(float64(size)))
	_ = obj.Set("text", engine.NewFunction("text", func(args []engine.Value) (engine.Value, error) {
		return gbase.ResolveValue(ctx, engine.Str(string(body)))
	}))
	_ = obj.Set("json", engine.NewFunction("json", func(args []engine.Value) (engine.Value, error) {
		jsonGlobal, err := ctx.Global().Get("JSON")
		if err != nil || !jsonGlobal.IsObject() {
			return gbase.RejectValue(ctx, "JSON not available")
		}
		jo, _ := jsonGlobal.AsObject()
		if pf, err := jo.Get("parse"); err == nil && pf.IsFunction() {
			if f, ok := pf.AsFunction(); ok {
				v, perr := f.Call([]engine.Value{engine.Str(string(body))})
				if perr != nil {
					return gbase.RejectValue(ctx, perr.Error())
				}
				return gbase.ResolveValue(ctx, v)
			}
		}
		return gbase.RejectValue(ctx, "JSON.parse failed")
	}))
	_ = obj.Set("arrayBuffer", engine.NewFunction("arrayBuffer", func(args []engine.Value) (engine.Value, error) {
		return gbase.ResolveValue(ctx, engine.NewBuffer(body))
	}))
	return obj
}

// parseS3List 解析 ListObjectsV2 XML 响应。
func parseS3List(body []byte) engine.Value {
	var result struct {
		Contents []struct {
			Key          string `xml:"Key"`
			LastModified string `xml:"LastModified"`
			ETag         string `xml:"ETag"`
			Size         int64  `xml:"Size"`
		} `xml:"Contents"`
	}
	_ = xml.Unmarshal(body, &result)
	arr := make([]engine.Value, 0, len(result.Contents))
	for _, c := range result.Contents {
		o := engine.NewObject()
		_ = o.Set("key", engine.Str(c.Key))
		_ = o.Set("size", engine.Number(float64(c.Size)))
		_ = o.Set("lastModified", engine.Str(c.LastModified))
		_ = o.Set("etag", engine.Str(strings.Trim(c.ETag, `"`)))
		arr = append(arr, o)
	}
	return engine.NewArray(arr)
}

// s3Str 取第 i 个参数并转为字符串。
func s3Str(args []engine.Value, i int) string {
	if len(args) <= i || args[i] == nil {
		return ""
	}
	return args[i].String()
}

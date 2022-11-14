package echox

import (
	`bytes`
	`crypto/aes`
	`crypto/cipher`
	`encoding/hex`
	`encoding/json`
	`io/ioutil`
	`net/http`
	`os`
	`strings`
	`time`

	`github.com/dgrijalva/jwt-go`
	`github.com/json-iterator/go`
	`github.com/labstack/echo/v4`
	`github.com/storezhang/gox`
)

const defaultIndent = "  "

type (
	EchoContext struct {
		echo.Context

		// JWT配置
		jwt *JWTConfig

		// AES配置
		aes *AESConfig
	}

	AESConfig struct {
		// 加密块
		block cipher.Block
		// 是否开启
		Enable bool
		// 加密密钥，需要是长度32的16进制字符串
		Key string
		// 不进行加密的接口前缀
		ExcludeRouterPrefixes []string
	}
)

func (a *AESConfig) Encrypt(plainText []byte) (hexed []byte, err error) {
	if a.block == nil {
		var hexKey []byte
		if hexKey, err = hex.DecodeString(a.Key); err != nil {
			return
		}

		if a.block, err = aes.NewCipher(hexKey); err != nil {
			return
		}
	}

	// 第一步，进行填充
	plainText = pkcs7Padding(plainText, a.block.BlockSize())

	// 第二步，生成随机偏移量
	iv := []byte(gox.RandString(a.block.BlockSize()))

	// 第三步，进行加密
	cipherText := make([]byte, len(plainText))
	cipher.NewCBCEncrypter(a.block, iv).CryptBlocks(cipherText, plainText)

	// 第四步，偏移量和密文进行拼接，生成最终的密文
	cipherText = append(iv, cipherText...)

	// 第五步，将密文转为16进制数据
	hexed = make([]byte, hex.EncodedLen(len(cipherText)))
	hex.Encode(hexed, cipherText)

	return
}

func pkcs7Padding(data []byte, size int) []byte {
	padding := size - len(data)%size

	return append(data, bytes.Repeat([]byte{byte(padding)}, padding)...)
}

func (ec *EchoContext) User() (user gox.BaseUser, err error) {
	var (
		token  string
		claims jwt.Claims
	)

	if token, err = ec.jwt.Extractor(ec.Context); nil != err {
		return
	}

	if claims, _, err = ec.jwt.Parse(token); nil != err {
		return
	}

	// 从JWT Token中反序列化User
	err = json.Unmarshal([]byte(claims.(*jwt.StandardClaims).Subject), &user)

	return
}

func (ec *EchoContext) GetUserFromToken(token string) (user gox.BaseUser, err error) {
	var claims jwt.Claims

	if claims, _, err = ec.jwt.Parse(token); nil != err {
		return
	}

	// 从JWT Token中反序列化User
	err = json.Unmarshal([]byte(claims.(*jwt.StandardClaims).Subject), &user)

	return
}

func (ec *EchoContext) JWTToken(domain string, user gox.BaseUser, expire time.Duration) (token string, id string, err error) {
	return ec.jwt.UserToken(domain, user, expire)
}

func (ec *EchoContext) HttpFile(file http.File) (err error) {
	defer func() {
		_ = file.Close()
	}()

	var fi os.FileInfo
	fi, err = file.Stat()
	if nil != err {
		return
	}

	http.ServeContent(ec.Response(), ec.Request(), fi.Name(), fi.ModTime(), file)

	return
}

func (ec *EchoContext) HttpAttachment(file http.File, name string) error {
	return ec.contentDisposition(file, name, gox.ContentDispositionTypeAttachment)
}

func (ec *EchoContext) HttpInline(file http.File, name string) error {
	return ec.contentDisposition(file, name, gox.ContentDispositionTypeInline)
}

func (ec *EchoContext) contentDisposition(file http.File, name string, dispositionType gox.ContentDispositionType) error {
	ec.Response().Header().Set(gox.HeaderContentDisposition, gox.ContentDisposition(name, dispositionType))

	return ec.HttpFile(file)
}

func (ec *EchoContext) NoContent(code int) (err error) {
	ec.Context.Response().WriteHeader(code)

	return
}

func (ec *EchoContext) JSON(code int, i interface{}) (err error) {
	if ec.aes.Enable {
		exist := false
		for _, router := range ec.aes.ExcludeRouterPrefixes {
			if strings.HasPrefix(ec.Context.Request().RequestURI, router) {
				exist = true

				break
			}
		}

		if !exist {
			var data []byte
			if data, err = json.Marshal(i); err != nil {
				return
			}

			if data, err = ec.aes.Encrypt(data); err != nil {
				return
			}

			_ = ec.Blob(code, echo.MIMETextPlain, data)

			return
		}
	}

	indent := ""
	if _, pretty := ec.QueryParams()["pretty"]; ec.Echo().Debug || pretty {
		indent = defaultIndent
	}
	return ec.json(code, i, indent)
}

func (ec *EchoContext) JSONPretty(code int, i interface{}, indent string) (err error) {
	return ec.json(code, i, indent)
}

func (ec *EchoContext) JSONBlob(code int, b []byte) (err error) {
	return ec.Blob(code, echo.MIMEApplicationJSONCharsetUTF8, b)
}

func (ec *EchoContext) JSONP(code int, callback string, i interface{}) (err error) {
	return ec.jsonPBlob(code, callback, i)
}

func (ec *EchoContext) JSONPBlob(code int, callback string, b []byte) (err error) {
	ec.writeContentType(echo.MIMEApplicationJavaScriptCharsetUTF8)
	ec.Response().WriteHeader(code)
	if _, err = ec.Response().Write([]byte(callback + "(")); err != nil {
		return
	}
	if _, err = ec.Response().Write(b); err != nil {
		return
	}
	_, err = ec.Response().Write([]byte(");"))

	return
}

func (ec *EchoContext) jsonPBlob(code int, callback string, i interface{}) (err error) {
	enc := jsoniter.NewEncoder(ec.Response())
	_, pretty := ec.QueryParams()["pretty"]
	if ec.Echo().Debug || pretty {
		enc.SetIndent("", "  ")
	}
	ec.writeContentType(echo.MIMEApplicationJavaScriptCharsetUTF8)
	ec.Response().WriteHeader(code)
	if _, err = ec.Response().Write([]byte(callback + "(")); err != nil {
		return
	}
	if err = enc.Encode(i); err != nil {
		return
	}
	if _, err = ec.Response().Write([]byte(");")); err != nil {
		return
	}

	return
}

func (ec *EchoContext) json(code int, i interface{}, indent string) error {
	enc := jsoniter.NewEncoder(ec.Response())
	if indent != "" {
		enc.SetIndent("", indent)
	}
	ec.writeContentType(echo.MIMEApplicationJSONCharsetUTF8)
	ec.Response().WriteHeader(code)

	return enc.Encode(i)
}

func (ec *EchoContext) writeContentType(value string) {
	header := ec.Response().Header()
	if "" == header.Get(echo.HeaderContentType) {
		header.Set(echo.HeaderContentType, value)
	}
}

// 获取有关联表的更新信息
func UpdateWithRelation(c echo.Context, bean interface{}, notCols ...string) (cols, otherCols []string, err error) {
	var (
		reqMap = make(map[string]interface{})
	)

	if err = UpdateMap(c, bean, &reqMap); nil != err {
		return
	}

	cols = make([]string, 0)
	otherCols = make([]string, 0)
	for key := range reqMap {
		if exists, _ := gox.IsInArray(key, notCols); exists {
			otherCols = append(otherCols, gox.UnderscoreName(key, false))
		} else {
			cols = append(cols, gox.UnderscoreName(key, false))
		}
	}

	if 0 == len(cols) && 0 == len(otherCols) {
		err = ErrNoUpdateParam
	}

	return
}

func UpdateInfo(c echo.Context, bean interface{}) (cols []string, err error) {
	var reqMap = make(map[string]interface{})

	if err = UpdateMap(c, bean, &reqMap); nil != err {
		return
	}

	cols = make([]string, 0)
	for key := range reqMap {
		cols = append(cols, gox.UnderscoreName(key, false))
	}

	if 0 == len(cols) {
		err = ErrNoUpdateParam
	}

	return
}

func UpdateMap(c echo.Context, bean, reqMap interface{}) (err error) {
	var body []byte

	if body, err = ioutil.ReadAll(c.Request().Body); nil != err {
		return
	}
	if err = json.Unmarshal(body, bean); nil != err {
		return
	}
	if err = json.Unmarshal(body, &reqMap); nil != err {
		return
	}

	return
}

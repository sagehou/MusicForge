package forge

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/md5"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const neteaseAppVersion = "3.1.3.203419"

// Public guest-device compatibility IDs sampled from the MIT-licensed protocol
// registry. EAPI rejects newly invented IDs with code 400; these are not login
// credentials. Source and license: THIRD_PARTY_NOTICES.md.
var neteaseGuestDevices = [...]string{
	"AA9955F5FE37BA7EAF48F8EF0C9966B28293CC8D6415CCD93549",
	"B48C45C7B6AE90E2F37D8EE2798D5FECCB482F0B9AC7C963A16A",
	"93FB70BC7633A97341D7F507932D76EE66A4FD1503C759CFCB06",
	"BEAC88FC6DE119A3CD8888FCE2375F504270BD38953E662F598E",
	"5DCDDF6CF18A8D1DE86B5741E5AC93DC75B90EFA67E6CA51290C",
	"B5EDE53C9A14E8DE830637F381CF009E10E17CEC862574BAE7AC",
	"932749F6A512FF01696F82B56C61B0FF20F7CE93C55C0836647C",
	"74A08D17095615934921B80484B904596A769C8324CF1ECA983C",
	"7BC7C918738CF3D1B63AF979967F6B0B2A2104D2E69FB6181979",
	"E442182D762E5DFF7C043BE841AA7C6A00DE5C470600E4C00991",
	"4F0C30CC04502635DF2E878979476F472E853A03D691578B62A5",
	"13EF40C7938439BD4B10FD4B79266E57ECBFA70F4A1885266B6D",
	"B2EF22003E53D089C41E7FE99D7CBC4D9AE1524C0F8CFF3D2013",
	"00249363664EB865F79D6C5605AAB1DA0E760BEA370CE6412EE5",
	"43A6D528AA3D35FC9912C7D8B2E2C0AA6A35823A7C0147E6CA49",
	"35CB9468CD76446FBE9F279EF84E4BFA3411934C886DC5A3C481",
	"861B4F838C6155F74459C4D7402AC3A5196AC9B46FF64E40A058",
	"F1A5F4CE55D55416D28FB56FC5C54E4CAFC3BEFC94E826F1743B",
	"403C509B9EC1C984327777C472E88C46B4C2AAC0B6ECE4E02F4F",
	"2EB24056A183A650130449C94FA8A99384152D67093400F84BE3",
	"8443E0D08F4591087698DFB8B464975FF6C6C9EB856C28509C3B",
	"5682F2FCB78927B03469599FF4244C62BE695F86EE0078A64CEF",
	"77DC1D3FE88B589004824F4E33FDDF1D350A9AEBDC35E1D5C93C",
	"AB98B77ACAA11B94CBD1EEE42B4C9A206F1276A33B06F1506EE8",
	"29B13AB14B89EA38CE833A93E7F68E1377DECED3C043426F9E83",
	"701A7E2F20EA27E4643A317B7A5067A74C13578B6EB7B09C0460",
	"E40097F8036C4A525074F1203D52D9A2A3171479BEF92AD3CD32",
	"EBED9D90F169ADBAE3012DF31A10D4CF998632947771E5BE2253",
	"BAF257A671BCB89872F7D60710EB799F260E478DC7DDD607C254",
	"F087EE573A605EFC467032177C996A6F02C8DF82E1FA3C439DB3",
	"4EBCD72E88AA321AA56ABDF9FAA28EA23C6C55CF130ADB2C3B2D",
	"F67D3B3E4CE5AD7750062C0C0B8D9448E217FF54571F44D658DB",
}

const neteaseCookieError = "NetEase Cookie file is unreadable or invalid; check its mount, permissions and MUSIC_U value"

type neteaseSession struct {
	cookies map[string]string
	expires time.Time
}

type neteaseSessionState struct {
	gate       chan struct{}
	session    *neteaseSession
	cookieHash [32]byte
	retryAt    time.Time
	reason     error
}

// A cancellable gate serializes initialization, credential rotation and requests.
// Guest credentials stay in memory so schema-2 persistence is unchanged.
func (a *App) neteaseCall(ctx context.Context, path string, params map[string]any, target any) error {
	a.lyricsMu.Lock()
	if a.netease == nil {
		a.netease = &neteaseSessionState{gate: make(chan struct{}, 1)}
	}
	state := a.netease
	a.lyricsMu.Unlock()
	select {
	case state.gate <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	defer func() { <-state.gate }()
	if err := ctx.Err(); err != nil {
		return err
	}

	var cookies map[string]string
	var hash [32]byte
	supplied := a.cfg.NetEaseCookieFile != ""
	if supplied {
		var err error
		cookies, hash, err = loadNetEaseCookies(a.cfg.NetEaseCookieFile)
		if err != nil {
			state.session = nil
			return err
		}
	}
	if hash != state.cookieHash {
		state.session, state.reason, state.retryAt = nil, nil, time.Time{}
		state.cookieHash = hash
	}
	if time.Now().Before(state.retryAt) {
		return state.reason
	}
	fail := func(code int, err error) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		delay := time.Minute
		switch code {
		case -462, -460:
			delay = 30 * time.Minute
		case 301, 302, 401, 403:
			state.session = nil
			if supplied {
				err = errors.New("NetEase login Cookie expired or was rejected; replace the Cookie file")
				delay = 30 * time.Minute
			} else {
				err = errors.New("NetEase guest session expired or was rejected; retry later")
				delay = time.Minute
			}
		}
		if delay > 0 {
			state.reason, state.retryAt = err, time.Now().Add(delay)
		}
		return err
	}
	if state.session == nil || !time.Now().Before(state.session.expires) {
		session := newNetEaseSession(cookies)
		if !supplied {
			session.cookies["deviceId"] = neteaseGuestDevices[rand.IntN(len(neteaseGuestDevices))]
			device := session.cookies["deviceId"]
			xor := []byte(device)
			const key = "3go8&$8*3*3h0k(2)2"
			for i := range xor {
				xor[i] ^= key[i%len(key)]
			}
			sum := md5.Sum(xor) // The anonymous-client protocol specifies MD5.
			username := base64.StdEncoding.EncodeToString([]byte(device + " " + base64.StdEncoding.EncodeToString(sum[:])))
			code, received, err := a.neteaseEAPI(ctx, "/eapi/register/anonimous", map[string]any{"username": username}, session, new(any))
			if err != nil {
				return fail(code, err)
			}
			for _, cookie := range received {
				if cookie.Name != "MUSIC_A" && cookie.Name != "NMTID" && cookie.Name != "__csrf" {
					continue
				}
				if cookie.Valid() != nil || cookie.MaxAge < 0 || !cookie.Expires.IsZero() && cookie.Expires.Before(time.Now()) {
					continue
				}
				session.cookies[cookie.Name] = cookie.Value
				if cookie.MaxAge > 0 && cookie.MaxAge < 10*24*60*60 {
					session.expires = minNetEaseExpiry(session.expires, time.Now().Add(time.Duration(cookie.MaxAge)*time.Second))
				}
				if !cookie.Expires.IsZero() {
					session.expires = minNetEaseExpiry(session.expires, cookie.Expires)
				}
			}
			if session.cookies["MUSIC_A"] == "" {
				return fail(0, errors.New("NetEase did not issue a guest session; retry later or configure a Cookie file"))
			}
		}
		state.session = session
	}
	code, _, err := a.neteaseEAPI(ctx, path, params, state.session, target)
	if err != nil {
		return fail(code, err)
	}
	return nil
}

func minNetEaseExpiry(left, right time.Time) time.Time {
	if right.Before(left) {
		return right
	}
	return left
}

func newNetEaseSession(cookies map[string]string) *neteaseSession {
	sign := strings.ToUpper(randomToken())
	mac := make([]string, 6)
	for i := range mac {
		mac[i] = sign[i*2 : i*2+2]
	}
	session := &neteaseSession{expires: time.Now().Add(10 * 24 * time.Hour), cookies: map[string]string{
		"os": "pc", "appver": neteaseAppVersion, "osver": "Microsoft-Windows-10--build-22621-64bit",
		"deviceId": strings.ToUpper(randomToken()[:52]), "channel": "netease", "WEVNSM": "1.0.0",
		"clientSign": strings.Join(mac, ":") + "@@@" + sign[12:20] + "@@@@@@" + randomToken(),
		"WNMCID":     randomToken()[:6] + "." + strconv.FormatInt(time.Now().UnixMilli(), 10) + ".01.0",
	}}
	for name, value := range cookies {
		session.cookies[name] = value
	}
	return session
}

// Read the mounted secret on every call, including during an auth cooldown. A
// changed file takes effect immediately; never store it in Settings or metadata.
func loadNetEaseCookies(path string) (map[string]string, [32]byte, error) {
	bad := func() (map[string]string, [32]byte, error) {
		return nil, [32]byte{}, errors.New(neteaseCookieError)
	}
	// Nonblocking open also bounds accidental FIFO/device mounts. Kubernetes
	// secret symlinks are supported; the opened target must be a regular file.
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return bad()
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > 16*1024 {
		return bad()
	}
	raw, err := io.ReadAll(io.LimitReader(f, 16*1024+1))
	if err != nil || len(raw) > 16*1024 {
		return bad()
	}
	value := strings.TrimSpace(string(raw))
	if value == "" || strings.ContainsAny(value, "\r\n") {
		return bad()
	}
	cookies := map[string]string{}
	for _, field := range strings.Split(value, ";") {
		if strings.TrimSpace(field) == "" {
			continue
		}
		name, content, ok := strings.Cut(strings.TrimSpace(field), "=")
		cookie := &http.Cookie{Name: name, Value: content}
		if !ok || cookie.Valid() != nil {
			return bad()
		}
		if _, duplicate := cookies[name]; duplicate {
			return bad()
		}
		cookies[name] = content
	}
	if cookies["MUSIC_U"] == "" {
		return bad()
	}
	return cookies, sha256.Sum256(raw), nil
}

// Implement the EAPI wire protocol with the standard library. Its fixed AES/MD5
// constants identify the public client protocol, not user credentials.
func neteaseEncrypt(raw []byte) []byte {
	block, _ := aes.NewCipher([]byte("e82ckenh8dichen8"))
	pad := aes.BlockSize - len(raw)%aes.BlockSize
	data := make([]byte, len(raw)+pad)
	copy(data, raw)
	copy(data[len(raw):], bytes.Repeat([]byte{byte(pad)}, pad))
	for i := 0; i < len(data); i += aes.BlockSize {
		block.Encrypt(data[i:i+aes.BlockSize], data[i:i+aes.BlockSize])
	}
	return data
}

func neteaseDecrypt(raw []byte) ([]byte, error) {
	bad := errors.New("netease returned invalid or oversized lyrics data")
	if len(raw) == 0 || len(raw)%aes.BlockSize != 0 || len(raw) > 8*maxLyricsBytes {
		return nil, bad
	}
	block, _ := aes.NewCipher([]byte("e82ckenh8dichen8"))
	data := make([]byte, len(raw))
	for i := 0; i < len(raw); i += aes.BlockSize {
		block.Decrypt(data[i:i+aes.BlockSize], raw[i:i+aes.BlockSize])
	}
	pad := int(data[len(data)-1])
	if pad < 1 || pad > aes.BlockSize || !bytes.Equal(data[len(data)-pad:], bytes.Repeat([]byte{byte(pad)}, pad)) {
		return nil, bad
	}
	return data[:len(data)-pad], nil
}

func (a *App) neteaseEAPI(ctx context.Context, path string, params map[string]any, session *neteaseSession, target any) (int, []*http.Cookie, error) {
	header := map[string]any{"requestId": 0}
	for _, name := range []string{"clientSign", "os", "appver", "deviceId", "osver"} {
		header[name] = session.cookies[name]
	}
	metadata, _ := json.Marshal(header)
	params["header"], params["e_r"] = string(metadata), true
	raw, err := json.Marshal(params)
	if err != nil {
		return 0, nil, errors.New("NetEase lyrics request contains invalid parameters")
	}
	canonical := strings.Replace(path, "/eapi/", "/api/", 1)
	signature := fmt.Sprintf("%x", md5.Sum([]byte("nobody"+canonical+"use"+string(raw)+"md5forencrypt")))
	body := "params=" + strings.ToUpper(hex.EncodeToString(neteaseEncrypt([]byte(canonical+"-36cd479b6b5-"+string(raw)+"-36cd479b6b5-"+signature))))
	request, err := http.NewRequestWithContext(ctx, "POST", "https://interface.music.163.com"+path, strings.NewReader(body))
	if err != nil {
		return 0, nil, errors.New("NetEase lyrics request contains invalid parameters")
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Referer", "https://music.163.com/")
	request.Header.Set("Origin", "orpheus://orpheus")
	request.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; WOW64) AppleWebKit/537.36 (KHTML, like Gecko) Safari/537.36 Chrome/91.0.4472.164 NeteaseMusicDesktop/"+neteaseAppVersion+" MusicForge/"+a.version)
	request.Header.Set("mconfig-info", `{"IuRPVVmc3WWul9fT":{"version":733184,"appver":"`+neteaseAppVersion+`"}}`)
	for name, value := range session.cookies {
		request.AddCookie(&http.Cookie{Name: name, Value: value})
	}
	response, raw, err := a.lyricsHTTPResponse("netease", request)
	if err != nil {
		if response != nil {
			return response.StatusCode, nil, err
		}
		return 0, nil, err
	}
	if !json.Valid(raw) {
		raw, err = neteaseDecrypt(raw)
		if err != nil {
			return 0, nil, err
		}
	}
	var status struct {
		Code int `json:"code"`
	}
	if json.Unmarshal(raw, &status) != nil || status.Code == 0 {
		return 0, nil, errors.New("netease returned invalid or oversized lyrics data")
	}
	if status.Code != 200 {
		return status.Code, nil, neteaseLyricsError(status.Code)
	}
	if json.Unmarshal(raw, target) != nil {
		return 0, nil, errors.New("netease returned invalid or oversized lyrics data")
	}
	return status.Code, response.Cookies(), nil
}

func neteaseLyricsError(code int) error {
	if code == -462 {
		return errors.New("NetEase requires account verification (code -462); update the Cookie file after verifying the account")
	}
	if code == -460 {
		return errors.New("NetEase temporarily restricted lyrics requests (code -460); retry later")
	}
	return fmt.Errorf("NetEase rejected the lyrics request (code %d)", code)
}

func (a *App) neteaseCandidates(ctx context.Context, query string) ([]lyricsCandidate, error) {
	params := map[string]any{"keyword": query, "scene": "NORMAL", "needCorrect": "true", "limit": "8", "offset": "0"}
	var response struct {
		Data struct {
			Resources []struct {
				Base struct {
					Song struct {
						ID       int64    `json:"id"`
						Name     string   `json:"name"`
						Aliases  []string `json:"alia"`
						Duration float64  `json:"dt"`
						Artists  []struct {
							Name    string   `json:"name"`
							Aliases []string `json:"alias"`
						} `json:"ar"`
						Album struct {
							Name string `json:"name"`
						} `json:"al"`
					} `json:"simpleSongData"`
				} `json:"baseInfo"`
			} `json:"resources"`
		} `json:"data"`
	}
	if err := a.neteaseCall(ctx, "/eapi/search/song/list/page", params, &response); err != nil {
		return nil, err
	}
	var result []lyricsCandidate
	for _, resource := range response.Data.Resources {
		song := resource.Base.Song
		if song.ID < 1 {
			continue
		}
		var artists, aliases []string
		for _, artist := range song.Artists {
			artists = append(artists, artist.Name)
			if len(song.Artists) == 1 {
				aliases = artist.Aliases
			}
		}
		result = append(result, lyricsCandidate{Title: song.Name, Artist: strings.Join(artists, " / "), Album: song.Album.Name, Duration: song.Duration / 1000,
			remoteID: strconv.FormatInt(song.ID, 10), titleAliases: song.Aliases, artistAliases: aliases})
	}
	return result, nil
}

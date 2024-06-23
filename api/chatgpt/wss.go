package chatgpt

import (
	"context"
	"encoding/json"
	http "github.com/bogdanfinn/fhttp"
	"github.com/bogdanfinn/tls-client/profiles"
	tls "github.com/bogdanfinn/utls"
	"github.com/gorilla/websocket"
	"github.com/leokwsw/go-chatgpt-api/api"
	"golang.org/x/net/proxy"
	"net"
	"net/url"
	"time"
)

func InitWebSocketConnect(token string, uuid string) error {
	connectInfo := findAvailableConnect(token, uuid)
	connect := connectInfo.Connect
	isExpired := connectInfo.Expire.IsZero() || time.Now().After(connectInfo.Expire)
	if connect == nil || isExpired {
		if connect != nil {
			connectInfo.Ticker.Stop()
			connect.Close()
			connectInfo.Connect = nil
		}
		wssURL, err := getWebSocketURL(token, 0)
		if err != nil {
			return err
		}
		err = CreateWebSocketConnection(wssURL, connectInfo, 0)
		if err != nil {
			return err
		}
		return nil
	} else {
		ctx, cancelFunc := context.WithTimeout(context.Background(), time.Millisecond*100)
		go func() {
			defer cancelFunc()
			for {
				_, _, err := connect.NextReader()
				if err != nil {
					break
				}
				if ctx.Err() != nil {
					break
				}
			}
		}()
		<-ctx.Done()
		err := ctx.Err()
		if err != nil {
			switch err {
			case context.Canceled:
				connectInfo.Ticker.Stop()
				connect.Close()
				connectInfo.Connect = nil
				connectInfo.Lock = false
				return InitWebSocketConnect(token, uuid)
			case context.DeadlineExceeded:
				return nil
			default:
				return nil
			}
		}
		return nil
	}
}

func findAvailableConnect(token string, uuid string) *api.ConnectInfo {
	for _, value := range api.ConnectPool[token] {
		if !value.Lock {
			value.Lock = true
			value.Uuid = uuid
			return value
		}
	}
	newConnectInfo := api.ConnectInfo{Uuid: uuid, Lock: true}
	api.ConnectPool[token] = append(api.ConnectPool[token], &newConnectInfo)
	return &newConnectInfo
}

func getWebSocketURL(token string, retry int) (string, error) {
	req, _ := http.NewRequest(http.MethodPost, ApiPrefix+"/register-websocket", nil)
	req.Header.Set("User-Agent", api.UserAgent)
	req.Header.Set("Accept", "*/*")
	if token != "" {
		req.Header.Set(api.AuthorizationHeader, api.GetAccessToken(token))
	}
	resp, err := api.Client.Do(req)
	if err != nil {
		if retry > 3 {
			return "", err
		}
		time.Sleep(time.Second)
		return getWebSocketURL(token, retry+1)
	}
	defer resp.Body.Close()
	var webSocketResp WebSocketResponse
	err = json.NewDecoder(resp.Body).Decode(&webSocketResp)
	if err != nil {
		return "", err
	}
	return webSocketResp.WssUrl, nil
}

type rawDialer interface {
	Dial(network string, addr string) (c net.Conn, err error)
}

func CreateWebSocketConnection(addr string, connectInfo *api.ConnectInfo, retry int) error {
	dialer := websocket.Dialer{
		HandshakeTimeout: 8 * time.Second,
		NetDialTLSContext: func(ctx context.Context, network string, addr string) (net.Conn, error) {
			host, _, _ := net.SplitHostPort(addr)
			config := &tls.Config{ServerName: host, OmitEmptyPsk: true}
			var rawDial rawDialer
			if api.ProxyUrl != "" {
				proxyURL, _ := url.Parse(api.ProxyUrl)
				rawDial, _ = proxy.FromURL(proxyURL, proxy.Direct)
			} else {
				rawDial = &net.Dialer{}
			}
			dialConn, err := rawDial.Dial(network, addr)
			if err != nil {
				return nil, err
			}
			client := tls.UClient(dialConn, config, profiles.Okhttp4Android13.GetClientHelloId(), false, true)
			return client, nil
		},
	}
	dialer.EnableCompression = true
	dialer.Subprotocols = []string{WebSocketProtocols}

	connect, _, err := dialer.Dial(addr, nil)

	if err != nil {
		if retry > 3 {
			return err
		}
		time.Sleep(time.Second) // wait 1s to recreate w
		return CreateWebSocketConnection(addr, connectInfo, retry+1)
	}

	connectInfo.Connect = connect
	connectInfo.Expire = time.Now().Add(time.Minute * 30)
	ticker := time.NewTicker(time.Second * 8)
	connectInfo.Ticker = ticker

	go func(ticker *time.Ticker) {
		defer ticker.Stop()
		for {
			<-ticker.C
			if err := connectInfo.Connect.WriteMessage(websocket.PingMessage, []byte{}); err != nil {
				connectInfo.Connect.Close()
				connectInfo.Connect = nil
				break
			}
		}
	}(ticker)
	return nil
}

func FindSpecConnection(token string, uuid string) *api.ConnectInfo {
	for _, value := range api.ConnectPool[token] {
		if value.Uuid == uuid {
			return value
		}
	}
	return &api.ConnectInfo{}
}

func UnlockSpecConn(token string, uuid string) {
	for _, value := range api.ConnectPool[token] {
		if value.Uuid == uuid {
			value.Lock = false
		}
	}
}

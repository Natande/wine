package ws

import (
	"container/list"
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gopub/wine"

	"github.com/gopub/conv"

	"github.com/gopub/errors"

	"github.com/gopub/types"

	"github.com/gorilla/websocket"
)

type ClientState int

const (
	Disconnected ClientState = iota
	Connecting
	Connected
	Closed
)

type Client struct {
	connTimeout      time.Duration
	pingInterval     time.Duration
	maxReconnBackoff time.Duration
	reconnBackoff    time.Duration

	addr string

	reqMu        sync.RWMutex
	reqs         *list.List
	reqC         chan struct{}
	reqIDToRespC map[int64]chan<- *Response

	connMu sync.Mutex
	conn   *websocket.Conn
	state  ClientState

	id int64

	HandshakeHandler func(rw ReadWriter) error
	Header           types.M
	pushDataC        chan interface{}

	ResultLogger func(req *Request, resp *Response)
}

func NewClient(addr string) *Client {
	c := &Client{
		connTimeout:      10 * time.Second,
		pingInterval:     10 * time.Second,
		maxReconnBackoff: 2 * time.Second,
		addr:             addr,
		reqs:             list.New(),
		reqC:             make(chan struct{}, 1),
		reqIDToRespC:     make(map[int64]chan<- *Response),
		state:            Disconnected,
		Header:           types.M{},
		pushDataC:        make(chan interface{}, 1),
		id:               1,
	}
	c.ResultLogger = c.logResult
	go c.start()
	return c
}

func (c *Client) nextID() int64 {
	atomic.AddInt64(&c.id, 2)
	return c.id
}

func (c *Client) start() {
	c.reconnBackoff = 100 * time.Millisecond
	for c.state != Closed {
		c.run()
		if c.reconnBackoff > 0 {
			time.Sleep(c.reconnBackoff)
		}
		c.reconnBackoff += 100 * time.Millisecond
		if c.reconnBackoff > c.maxReconnBackoff {
			c.reconnBackoff = c.maxReconnBackoff
		}
	}
}

func (c *Client) run() {
	c.state = Connecting
	ctx, cancel := context.WithTimeout(context.Background(), c.connTimeout)
	conn, _, err := websocket.DefaultDialer.DialContext(ctx, c.addr, nil)
	if err != nil {
		cancel()
		logger.Errorf("Cannot connect %s: %v", c.addr, err)
		c.state = Disconnected
		return
	}
	cancel()
	if c.HandshakeHandler != nil {
		if err = c.HandshakeHandler(conn); err != nil {
			logger.Errorf("Cannot handshake: %v", err)
			conn.Close()
			c.state = Disconnected
			return
		}
	}
	c.conn = conn
	c.state = Connected
	done := make(chan struct{}, 1)
	go c.read(done)
	c.write(done)
	c.conn.Close()
	c.state = Disconnected
}

func (c *Client) read(done chan<- struct{}) {
	defer logger.Debug("Exited read loop")
	for {
		resp := new(Response)
		err := c.conn.ReadJSON(resp)
		if err != nil {
			logger.Errorf("Cannot read: %v", err)
			done <- struct{}{}
			return
		}
		if resp.IsPush() && resp.Data != nil {
			select {
			case c.pushDataC <- resp.Data:
				break
			default:
				break
			}
		}
		c.reqMu.RLock()
		if ch, ok := c.reqIDToRespC[resp.ID]; ok {
			ch <- resp
			delete(c.reqIDToRespC, resp.ID)
		}
		c.reqMu.RUnlock()
	}
}

func (c *Client) write(done <-chan struct{}) {
	defer logger.Debug("Exited write loop")
	t := time.NewTicker(c.pingInterval)
	m := NewNetworkMonitor()
	defer m.Stop()
	defer t.Stop()
	for {
		select {
		case <-t.C:
			if err := c.conn.WriteJSON(&Request{}); err != nil {
				logger.Errorf("Cannot ping: %v", err)
				c.reconnBackoff = 0
				return
			}
			logger.Debugf("Ping")
		case <-m.C:
			c.reconnBackoff = 0
			return
		case <-done:
			c.reconnBackoff = 0
			return
		case <-c.reqC:
			c.reqMu.Lock()
			for it := c.reqs.Front(); it != nil; {
				req := it.Value.(*Request)
				next := it.Next()
				c.reqs.Remove(it)
				it = next
				if err := c.conn.WriteJSON(req); err != nil {
					logger.Errorf("Cannot write %s: %v", req.Name, err)
					if respC, ok := c.reqIDToRespC[req.ID]; ok {
						resp := &Response{ID: req.ID, Error: errors.Format(0, err.Error())}
						select {
						case respC <- resp:
							break
						default:
							break
						}
						delete(c.reqIDToRespC, req.ID)
					}
					c.reqMu.Unlock()
					return
				}
			}
			c.reqMu.Unlock()
		}
	}
}

func (c *Client) Call(ctx context.Context, name string, params interface{}, result interface{}) error {
	if c.state == Closed {
		return errors.New("client is closed")
	}
	req := &Request{
		ID:   c.nextID(),
		Name: name,
		Body: params,

		createdAt: time.Now(),
	}
	if len(c.Header) > 0 {
		req.Header = c.Header
	}
	respC := make(chan *Response, 1)
	defer close(respC)
	c.reqMu.Lock()
	c.reqs.PushBack(req)
	c.reqIDToRespC[req.ID] = respC
	c.reqMu.Unlock()
	select {
	case c.reqC <- struct{}{}:
		break
	default:
		break
	}

	select {
	case <-ctx.Done():
		if c.ResultLogger != nil {
			c.ResultLogger(req, &Response{ID: req.ID, Error: errors.Format(0, ctx.Err().Error())})
		}
		return ctx.Err()
	case resp := <-respC:
		if c.ResultLogger != nil {
			c.ResultLogger(req, resp)
		}
		if resp.Error != nil {
			return resp.Error
		}
		if result != nil {
			if resp.Data != nil {
				return conv.Assign(result, resp.Data)
			}
			return errors.New("no data")
		}
		return nil
	}
}

func (c *Client) Close() {
	c.state = Closed
	close(c.pushDataC)
}

func (c *Client) SetConnTimeout(t time.Duration) {
	if t < time.Second {
		t = time.Second
	}
	c.connTimeout = t
}

func (c *Client) SetPingInterval(t time.Duration) {
	if t < time.Second {
		t = time.Second
	}
	c.pingInterval = t
}

func (c *Client) SetMaxReconnBackoff(t time.Duration) {
	if t <= 0 {
		t = 0
	}
	c.maxReconnBackoff = t
}

func (c *Client) PushDataC() <-chan interface{} {
	return c.pushDataC
}

func (c *Client) GetServerTime(ctx context.Context) (time.Time, error) {
	var res struct {
		Timestamp int64 `json:"timestamp"`
	}
	err := c.Call(ctx, methodGetDate, nil, &res)
	if err != nil {
		return time.Time{}, err
	}
	return time.Unix(res.Timestamp, 0), nil
}

func (c *Client) logResult(req *Request, resp *Response) {
	cost := time.Since(req.createdAt)
	if resp.Error != nil {
		logger.Errorf("%d %s | %v | %v | %v", resp.ID, req.Name, wine.JSONString(req.Body), resp.Error, cost)
	} else {
		logger.Infof("%d %s | %v", resp.ID, req.Name, cost)
	}
}

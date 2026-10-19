package whatsappprovider

import (
	"context"
	"fmt"
	"sync"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types"
	"go.uber.org/zap"

	"github.com/edcnogueira/meowmcp/internal/domain"
	"github.com/edcnogueira/meowmcp/internal/logging"
)

const DeviceName = "meowmcp"

const firstQRTimeout = 20 * time.Second

type Update struct {
	Messages []domain.Message
	Chats    []domain.Chat
	Renames  []domain.Chat
	Edits    []Edit
	Live     bool
}

type Edit struct {
	ChatJID   string
	MessageID string
	Text      string
}

type UpdateHandler func(ctx context.Context, u Update)

type Provider struct {
	container *sqlstore.Container
	logger    *zap.Logger
	onUpdate  UpdateHandler

	loginMu sync.Mutex

	mu         sync.Mutex
	client     *whatsmeow.Client
	state      domain.ConnectionState
	pairing    *domain.Pairing
	lastErr    string
	groupNames map[types.JID]struct{}

	runCtx context.Context
}

func New(ctx context.Context, dsn string, logger *zap.Logger) (*Provider, error) {
	store.SetOSInfo(DeviceName, [3]uint32{1, 0, 0})
	container, err := sqlstore.New(ctx, "sqlite", dsn, logging.Whatsmeow(logger, "whatsmeow.db"))
	if err != nil {
		return nil, fmt.Errorf("failed to open whatsmeow store: %w", err)
	}
	device, err := container.GetFirstDevice(ctx)
	if err != nil {
		_ = container.Close()
		return nil, fmt.Errorf("failed to load whatsmeow device: %w", err)
	}
	p := &Provider{
		container:  container,
		logger:     logger,
		onUpdate:   func(context.Context, Update) {},
		state:      domain.ConnectionStateLoggedOut,
		groupNames: make(map[types.JID]struct{}),
		runCtx:     context.Background(),
	}
	if device.ID != nil {
		p.state = domain.ConnectionStateConnecting
	}
	p.client = p.newClient(device)
	return p, nil
}

func (p *Provider) SetUpdateHandler(h UpdateHandler) {
	p.onUpdate = h
}

func (p *Provider) newClient(device *store.Device) *whatsmeow.Client {
	cli := whatsmeow.NewClient(device, logging.Whatsmeow(p.logger, "whatsmeow"))
	cli.AddEventHandler(func(evt any) { p.handleEvent(cli, evt) })
	return cli
}

func (p *Provider) Start(ctx context.Context) {
	p.mu.Lock()
	p.runCtx = ctx
	cli := p.client
	paired := cli.Store.ID != nil
	if paired {
		p.state = domain.ConnectionStateConnecting
	}
	p.mu.Unlock()

	if paired {
		go p.connectWithRetry(ctx, cli)
	}
}

func (p *Provider) connectWithRetry(ctx context.Context, cli *whatsmeow.Client) {
	backoff := time.Second
	for {
		err := cli.Connect()
		if err == nil {
			return
		}
		p.logger.Warn("failed to connect to whatsapp, retrying", zap.Error(err), zap.Duration("in", backoff))
		p.setLastErr(fmt.Sprintf("connect failed: %v", err))
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, time.Minute)
		if p.currentClient() != cli || cli.Store.ID == nil {
			return
		}
	}
}

func (p *Provider) Stop() error {
	p.mu.Lock()
	cli := p.client
	p.mu.Unlock()
	cli.Disconnect()
	return p.container.Close()
}

func (p *Provider) Status() domain.Status {
	p.mu.Lock()
	defer p.mu.Unlock()
	st := domain.Status{State: p.state, LastError: p.lastErr}
	if id := p.client.Store.ID; id != nil {
		st.JID = id.ToNonAD().String()
		st.PushName = p.client.Store.PushName
	}
	if p.pairing != nil {
		pairing := *p.pairing
		st.Pairing = &pairing
	}
	return st
}

func (p *Provider) Login(ctx context.Context, phone string) (domain.Status, error) {
	var digits string
	if phone != "" {
		var err error
		if digits, err = NormalizePhone(phone); err != nil {
			return domain.Status{}, err
		}
	}

	p.loginMu.Lock()
	defer p.loginMu.Unlock()

	p.mu.Lock()
	cli := p.client
	paired := cli.Store.ID != nil
	pairingActive := p.state == domain.ConnectionStatePairing && p.pairing != nil && p.pairing.QRCode != ""
	needCode := pairingActive && digits != "" && p.pairing.PairingPhone != digits
	p.mu.Unlock()

	switch {
	case paired:
		return p.Status(), domain.ErrAlreadyLoggedIn
	case pairingActive:
		if needCode {
			if err := p.requestPairingCode(ctx, cli, digits); err != nil {
				return p.Status(), err
			}
		}
		return p.Status(), nil
	}

	cli.Disconnect()
	if cli.Store.Deleted {
		cli = p.newClient(p.container.NewDevice())
	}
	pairCtx, cancelPairing := context.WithCancel(p.runCtx)
	qrChan, err := cli.GetQRChannel(pairCtx)
	if err != nil {
		cancelPairing()
		return domain.Status{}, fmt.Errorf("failed to start qr pairing: %w", err)
	}

	p.mu.Lock()
	p.client = cli
	p.state = domain.ConnectionStatePairing
	p.pairing = &domain.Pairing{}
	p.lastErr = ""
	p.mu.Unlock()

	firstCode := make(chan struct{})
	go func() {
		defer cancelPairing()
		p.consumeQR(cli, qrChan, firstCode)
	}()
	if err := cli.Connect(); err != nil {
		cancelPairing()
		p.setState(cli, domain.ConnectionStateLoggedOut, fmt.Sprintf("connect failed: %v", err))
		return p.Status(), fmt.Errorf("failed to connect for pairing: %w", err)
	}

	select {
	case <-firstCode:
	case <-time.After(firstQRTimeout):
		return p.Status(), fmt.Errorf("timed out waiting for whatsapp to issue a qr code")
	case <-ctx.Done():
		return p.Status(), ctx.Err()
	}

	if st := p.Status(); st.State != domain.ConnectionStatePairing {
		return st, fmt.Errorf("pairing ended before a code was issued: %s", st.LastError)
	}
	if digits != "" {
		if err := p.requestPairingCode(ctx, cli, digits); err != nil {
			return p.Status(), err
		}
	}
	return p.Status(), nil
}

func (p *Provider) requestPairingCode(ctx context.Context, cli *whatsmeow.Client, digits string) error {
	code, err := cli.PairPhone(ctx, digits, true, whatsmeow.PairClientChrome, "Chrome (Linux)")
	if err != nil {
		return fmt.Errorf("failed to request pairing code: %w", err)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.pairing != nil {
		p.pairing.PairingCode = code
		p.pairing.PairingPhone = digits
	}
	return nil
}

func (p *Provider) consumeQR(cli *whatsmeow.Client, qrChan <-chan whatsmeow.QRChannelItem, firstCode chan struct{}) {
	var once sync.Once
	signal := func() { once.Do(func() { close(firstCode) }) }
	defer signal()

	for item := range qrChan {
		p.mu.Lock()
		if p.client != cli {
			p.mu.Unlock()
			return
		}
		switch item.Event {
		case whatsmeow.QRChannelEventCode:
			if p.pairing == nil {
				p.pairing = &domain.Pairing{}
			}
			p.pairing.QRCode = item.Code
			p.pairing.QRExpiresAt = time.Now().Add(item.Timeout)
			p.mu.Unlock()
			signal()
			continue
		case whatsmeow.QRChannelSuccess.Event:
			p.state = domain.ConnectionStateConnecting
			p.pairing = nil
			p.lastErr = ""
			p.logger.Info("whatsapp pairing succeeded")
		case whatsmeow.QRChannelTimeout.Event:
			p.state = domain.ConnectionStateLoggedOut
			p.pairing = nil
			p.lastErr = "pairing expired before the QR/pairing code was used; log in again for a new code"
		case whatsmeow.QRChannelEventError:
			p.state = domain.ConnectionStateLoggedOut
			p.pairing = nil
			p.lastErr = fmt.Sprintf("pairing failed: %v", item.Error)
		default:
			p.state = domain.ConnectionStateLoggedOut
			p.pairing = nil
			p.lastErr = "pairing failed: " + item.Event
		}
		p.mu.Unlock()
	}
}

func (p *Provider) Logout(ctx context.Context) error {
	cli := p.currentClient()
	if cli.Store.ID == nil {
		return domain.ErrNotLoggedIn
	}
	if err := cli.Logout(ctx); err != nil {
		return fmt.Errorf("failed to log out: %w", err)
	}
	p.mu.Lock()
	p.state = domain.ConnectionStateLoggedOut
	p.pairing = nil
	p.lastErr = ""
	p.mu.Unlock()
	return nil
}

func (p *Provider) currentClient() *whatsmeow.Client {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.client
}

func (p *Provider) readyClient() (*whatsmeow.Client, error) {
	cli := p.currentClient()
	switch {
	case cli.Store.ID == nil:
		return nil, domain.ErrNotLoggedIn
	case !cli.IsConnected() || !cli.IsLoggedIn():
		return nil, domain.ErrNotConnected
	}
	return cli, nil
}

func (p *Provider) setState(cli *whatsmeow.Client, state domain.ConnectionState, lastErr string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.client != cli {
		return
	}
	p.state = state
	p.lastErr = lastErr
	if state != domain.ConnectionStatePairing {
		p.pairing = nil
	}
}

func (p *Provider) setLastErr(msg string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.lastErr = msg
}

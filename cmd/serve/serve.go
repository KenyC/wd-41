package serve

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"path"

	"github.com/baalimago/go_away_boilerplate/pkg/ancli"
	"github.com/baalimago/wd-40/internal/wsinject"
	"golang.org/x/net/websocket"
)

type command struct {
	binPath string
	// master, as in adjective 'master record' non-slavery kind
	masterPath string
	mirrorPath string
	port       *int
	wsPath     *string
	flagset    *flag.FlagSet
	fileserver *wsinject.Fileserver
}

func Command() *command {
	r, _ := os.Executable()
	return &command{
		binPath: r,
	}
}

func (c *command) Setup() error {
	relPath := ""
	if len(c.flagset.Args()) == 0 {
		wd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("failed to get exec path: %w", err)
		}
		relPath = wd
	} else {
		relPath = c.flagset.Arg(0)
	}
	c.masterPath = path.Clean(relPath)

	if c.masterPath != "" {
		c.fileserver = wsinject.NewFileServer(*c.port, *c.wsPath)
		mirrorPath, err := c.fileserver.Setup(c.masterPath)
		if err != nil {
			return fmt.Errorf("failed to setup websocket injected mirror filesystem: %v", err)
		}
		c.mirrorPath = mirrorPath
	}

	return nil
}

func (c *command) Run(ctx context.Context) error {
	mux := http.NewServeMux()
	fsh := http.FileServer(http.Dir(c.mirrorPath))
	fsh = slogHandler(fsh)
	mux.Handle("/", fsh)

	ancli.PrintfOK("setting up websocket host on path: '%v'", *c.wsPath)
	mux.Handle(*c.wsPath, websocket.Handler(c.fileserver.WsHandler))

	s := http.Server{
		Addr:        fmt.Sprintf(":%v", *c.port),
		Handler:     mux,
		ReadTimeout: 0,
	}
	serverErrChan := make(chan error, 1)
	fsErrChan := make(chan error, 1)
	go func() {
		ancli.PrintfOK("now serving directory: '%v' on port: '%v', mirror dir is: '%v'", c.masterPath, *c.port, c.mirrorPath)
		err := s.ListenAndServe()
		if !errors.Is(err, http.ErrServerClosed) {
			serverErrChan <- err
		}
	}()
	go func() {
		ancli.PrintOK("starting fsnotify file detector")
		err := c.fileserver.Start(ctx)
		if err != nil {
			fsErrChan <- err
		}
	}()
	var retErr error
	select {
	case <-ctx.Done():
	case serveErr := <-serverErrChan:
		retErr = serveErr
		break
	case fsErr := <-fsErrChan:
		retErr = fsErr
		break
	}
	ancli.PrintNotice("initiating webserver graceful shutdown")
	s.Shutdown(ctx)
	ancli.PrintOK("shutdown complete")
	return retErr
}

func (c *command) Help() string {
	return "Serve some filesystem. Set the directory as the second argument: wd-40 serve <dir>. If omitted, current wd will be used."
}

func (c *command) Describe() string {
	return fmt.Sprintf("a webserver. Usage: '%v serve <path>'. If <path> is left unfilled, current pwd will be used.", c.binPath)
}

func (c *command) Flagset() *flag.FlagSet {
	fs := flag.NewFlagSet("server", flag.ExitOnError)
	c.port = fs.Int("port", 8080, "port to serve http server on")
	c.wsPath = fs.String("wsPort", "/delta-streamer-ws", "the path which the delta streamer websocket should be hosted on")
	c.flagset = fs
	return fs
}
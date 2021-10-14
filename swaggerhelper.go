package main

import (
	"crypto/tls"
	"embed"
	"fmt"
	"io"
	"io/fs"
	"io/ioutil"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/akamensky/argparse"
	"github.com/buger/jsonparser"
	"github.com/fatih/color"
	"github.com/labstack/echo"
	"github.com/labstack/echo/middleware"
)

type (
	// ProxyConfig defines the config for Proxy middleware.
	ProxyConfig struct {
		// Skipper defines a function to skip middleware.
		Skipper middleware.Skipper

		// Balancer defines a load balancing technique.
		// Required.
		Balancer middleware.ProxyBalancer

		// Rewrite defines URL path rewrite rules. The values captured in asterisk can be
		// retrieved by index e.g. $1, $2 and so on.
		// Examples:
		// "/old":              "/new",
		// "/api/*":            "/$1",
		// "/js/*":             "/public/javascripts/$1",
		// "/users/*/orders/*": "/user/$1/order/$2",
		Rewrite map[string]string

		// Context key to store selected ProxyTarget into context.
		// Optional. Default value "target".
		ContextKey string

		// To customize the transport to remote.
		// Examples: If custom TLS certificates are required.
		Transport http.RoundTripper

		rewriteRegex map[*regexp.Regexp]string
	}
)

//go:embed swagger-ui
var swaggerUIFiles embed.FS

func getSwaggerUIFiles() http.FileSystem {
	fsys, err := fs.Sub(swaggerUIFiles, "swagger-ui")
	if err != nil {
		panic(err)
	}
	return http.FS(fsys)
}

const (
	banner = `

	╔══╗─────────────╔╗╔╗
	║══╬╦╦╦═╗╔═╦═╦═╦╦╣╚╝╠═╦╗╔═╦═╦╦╗
	╠══║║║║╬╚╣╬║╬║╩╣╔╣╔╗║╩╣╚╣╬║╩╣╔╝
	╚══╩══╩══╬╗╠╗╠═╩╝╚╝╚╩═╩═╣╔╩═╩╝
	─────────╚═╩═╝──────────╚╝
`
)

var ServerHost string = ""

func main() {
	parser := argparse.NewParser("SwaggerHelper", "")
	var listenAddress *string = parser.String("L", "listen", &argparse.Options{Required: false, Default: "127.0.0.1:1323", Help: "bind address."})
	var swaggerPath *string = parser.String("F", "apifile", &argparse.Options{Required: true, Help: "swagger-ui api-docs file path."})
	var serverRoot *string = parser.String("S", "serverroot", &argparse.Options{Required: false, Default: "", Help: "server override."})
	err := parser.Parse(os.Args)
	exit_on_error("[PARSER ERROR]", err)
	useServerOverride := *serverRoot != ""
	e := echo.New()
	e.HideBanner = true
	e.GET("/swagger.json", func(c echo.Context) error {
		data := getContent(*swaggerPath)
		if useServerOverride {
			result, _ := jsonparser.Set(data, []byte("\"\""), "host")
			final, _ := jsonparser.Set(result, []byte("\"/backend-api\""), "basePath")
			return c.JSONBlob(http.StatusOK, final)
		}
		return c.JSONBlob(http.StatusOK, data)
	})
	if useServerOverride {
		backend, err := url.Parse(*serverRoot)
		if err != nil {
			e.Logger.Fatal(err)
		}
		ServerHost = backend.Host
		targets := []*middleware.ProxyTarget{
			{
				URL: backend,
			},
		}
		proxyBackend := e.Group("/backend-api")
		proxyBackend.Use(func(next echo.HandlerFunc) echo.HandlerFunc {
			return func(ctx echo.Context) error {
				ctx.Request().Header.Set("Host", ServerHost)
				ctx.Request().Header.Del("Origin")
				ctx.Request().Header.Del("Referer")
				ctx.Request().Host = ServerHost
				return next(ctx)
			}
		})
		proxyBackend.Use(ProxyWithConfig(ProxyConfig{
			Balancer: middleware.NewRoundRobinBalancer(targets),
			Rewrite: map[string]string{
				"^/backend-api/*": "/$1",
			},
			Transport: &http.Transport{
				TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
			},
		},
		))
	}

	assetHandler := http.FileServer(getSwaggerUIFiles())
	e.GET("/*", echo.WrapHandler(assetHandler))
	println(banner)
	e.Logger.Fatal(e.Start(*listenAddress))
}

func captureTokens(pattern *regexp.Regexp, input string) *strings.Replacer {
	groups := pattern.FindAllStringSubmatch(input, -1)
	if groups == nil {
		return nil
	}
	values := groups[0][1:]
	replace := make([]string, 2*len(values))
	for i, v := range values {
		j := 2 * i
		replace[j] = "$" + strconv.Itoa(i+1)
		replace[j+1] = v
	}
	return strings.NewReplacer(replace...)
}

// ProxyWithConfig returns a Proxy middleware with config.
// See: `Proxy()`
func ProxyWithConfig(config ProxyConfig) echo.MiddlewareFunc {
	// Defaults
	if config.Skipper == nil {
		config.Skipper = middleware.DefaultLoggerConfig.Skipper
	}
	if config.Balancer == nil {
		panic("echo: proxy middleware requires balancer")
	}
	config.rewriteRegex = map[*regexp.Regexp]string{}

	// Initialize
	for k, v := range config.Rewrite {
		k = strings.Replace(k, "*", "(\\S*)", -1)
		config.rewriteRegex[regexp.MustCompile(k)] = v
	}

	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) (err error) {
			if config.Skipper(c) {
				return next(c)
			}

			req := c.Request()
			res := c.Response()
			tgt := config.Balancer.Next(c)
			c.Set(config.ContextKey, tgt)

			for k, v := range config.rewriteRegex {
				replacer := captureTokens(k, req.URL.Path)
				if replacer != nil {
					req.URL.Path = replacer.Replace(v)
				}
			}
			// Proxy
			switch {
			case c.IsWebSocket():
				proxyRaw(tgt, c).ServeHTTP(res, req)
			case req.Header.Get(echo.HeaderAccept) == "text/event-stream":
			default:
				proxyHTTP(tgt, c, config).ServeHTTP(res, req)
			}

			return
		}
	}
}

func proxyHTTP(tgt *middleware.ProxyTarget, c echo.Context, config ProxyConfig) http.Handler {
	proxy := httputil.NewSingleHostReverseProxy(tgt.URL)
	proxy.ErrorHandler = func(resp http.ResponseWriter, req *http.Request, err error) {
		desc := tgt.URL.String()
		if tgt.Name != "" {
			desc = fmt.Sprintf("%s(%s)", tgt.Name, tgt.URL.String())
		}
		c.Logger().Errorf("remote %s unreachable, could not forward: %v", desc, err)
		c.Error(echo.NewHTTPError(http.StatusServiceUnavailable))
	}
	proxy.Transport = config.Transport
	return proxy
}

func proxyRaw(t *middleware.ProxyTarget, c echo.Context) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		in, _, err := c.Response().Hijack()
		if err != nil {
			c.Error(fmt.Errorf("proxy raw, hijack error=%v, url=%s", t.URL, err))
			return
		}
		defer in.Close()

		out, err := net.Dial("tcp", t.URL.Host)
		if err != nil {
			he := echo.NewHTTPError(http.StatusBadGateway, fmt.Sprintf("proxy raw, dial error=%v, url=%s", t.URL, err))
			c.Error(he)
			return
		}
		defer out.Close()

		// Write header
		err = r.Write(out)
		if err != nil {
			he := echo.NewHTTPError(http.StatusBadGateway, fmt.Sprintf("proxy raw, request header copy error=%v, url=%s", t.URL, err))
			c.Error(he)
			return
		}

		errCh := make(chan error, 2)
		cp := func(dst io.Writer, src io.Reader) {
			_, err = io.Copy(dst, src)
			errCh <- err
		}

		go cp(out, in)
		go cp(in, out)
		err = <-errCh
		if err != nil && err != io.EOF {
			c.Logger().Errorf("proxy raw, copy body error=%v, url=%s", t.URL, err)
		}
	})
}

func getContent(filepath string) []byte {
	content, err := ioutil.ReadFile(filepath)
	if err == nil {
		return content
	}
	return nil
}

func exit_on_error(message string, err error) {
	if err != nil {
		color.Red(message)
		fmt.Println(err)
		os.Exit(0)
	}
}

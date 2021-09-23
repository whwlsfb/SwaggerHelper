package main

import (
	"embed"
	"fmt"
	"io/fs"
	"io/ioutil"
	"net/http"
	"net/url"
	"os"

	"github.com/akamensky/argparse"
	"github.com/buger/jsonparser"
	"github.com/fatih/color"
	"github.com/labstack/echo"
	"github.com/labstack/echo/middleware"
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

func main() {
	parser := argparse.NewParser("SwaggerHelper", "")
	var listenAddress *string = parser.String("L", "listen", &argparse.Options{Required: false, Default: "127.0.0.1:1323", Help: "bind address."})
	var swaggerPath *string = parser.String("F", "apifile", &argparse.Options{Required: true, Help: "swagger-ui api-docs file path."})
	var serverRoot *string = parser.String("S", "serverroot", &argparse.Options{Required: false, Default: "", Help: "server override."})
	err := parser.Parse(os.Args)
	exit_on_error("[PARSER ERROR]", err)

	e := echo.New()
	e.GET("/swagger.json", func(c echo.Context) error {
		data := getContent(*swaggerPath)
		result, _ := jsonparser.Set(data, []byte("\"\""), "host")
		final, _ := jsonparser.Set(result, []byte("\"/backend-api\""), "basePath")
		return c.JSONBlob(http.StatusOK, final)
	})

	backend, err := url.Parse(*serverRoot)
	if err != nil {
		e.Logger.Fatal(err)
	}
	targets := []*middleware.ProxyTarget{
		{
			URL: backend,
		},
	}
	proxyBackend := e.Group("/backend-api")
	proxyBackend.Use(middleware.ProxyWithConfig(middleware.ProxyConfig{
		Balancer: middleware.NewRoundRobinBalancer(targets),
		Rewrite: map[string]string{
			"^/backend-api/*": "/$1",
		},
	},
	))

	assetHandler := http.FileServer(getSwaggerUIFiles())
	e.GET("/*", echo.WrapHandler(assetHandler))
	e.Logger.Fatal(e.Start(*listenAddress))
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

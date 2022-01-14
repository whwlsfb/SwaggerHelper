# SwaggerHelper

SwaggerHelper是用于启动本地保存的api-docs.json文档的小工具，例如目标系统只开启了api-docs接口而没有开启Swagger-UI，或在对系统进行**二次**渗透测试时,若目标关闭了Swagger-UI，则可使用本工具在本地启动接口文档（前提是api-docs文档已离线保存在本地），直接调用目标接口。

# 编译

> 编译该工具需要go 1.16或更高版本

`$ go build`

# 使用方法

```
usage: SwaggerHelper [-h|--help] [-L|--listen "<value>"] -F|--apifile "<value>"
                     [-S|--serverroot "<value>"]

Arguments:

  -h  --help        Print help information
  -L  --listen      bind address.. Default: 127.0.0.1:1323
  -F  --apifile     swagger-ui api-docs file path.
  -S  --serverroot  server override.. Default:

```

若不需要覆盖api-docs内部的host和bashPath，直接执行如下命令：

`SwaggerHelper -F /to/path/api-docs.json`

若因为CORS限制或服务器地址更改需要另外指定API根路径的，执行如下命令：

`SwaggerHelper -F /to/path/api-docs.json -S http://1.2.3.4/api`

启动完成后用浏览器访问监听地址即可。

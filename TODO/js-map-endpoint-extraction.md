# katana Source Map 端点提取实施方案

## 目标与开关语义

- 新增独立 CLI 参数 `-jsmap`，默认关闭；配置 API 增加对应的 `ScrapeJSMapResponses` 布尔选项。
- `-jsmap` 自行扫描外部 JS 文件及 HTML 内联脚本中的 `sourceMappingURL` 声明，定位、抓取并解析 map。它不依赖 `-jc` 或 `-jsl`。
- `-jc` 与 `-jsl` 保持现有职责，分别解析普通 JS 响应。Source Map 源码在 `-jsmap` 启用时至少走轻量 regex 提取；若同时启用 `-jsl`，再运行 jsluice 提取并合并去重。三种开关可独立或组合使用。
- 只解出端点候选并交给现有导航队列；沿用现有请求去重、作用域、深度、响应大小、超时和输出过滤。

## 结构与数据流

保持解析逻辑分层，避免将发现、解码、URL 归一化和端点提取写进一个大函数：

1. **引用发现**：独立函数扫描 JS 注释中的 `//# sourceMappingURL=...`、`//@` 和块注释形式；仅处理可解析的 HTTP(S)、根相对及文件相对 URL。首版跳过 `data:` 内嵌 map。
2. **调度与过滤**：将 map URL 转成带有 Source Map 来源标记的导航请求。`-jsmap` 开启时，仅允许由 Source Map 引用产生的 `.map` 请求绕过默认 `.map` 扩展过滤；直接指定的 map seed 也可处理。继续尊重用户显式扩展过滤和现有作用域检查。
3. **Map 解码**：独立解码器识别 Source Map v3 普通 map 与 indexed map；递归处理内嵌 sections，并为外部 section map 生成有来源标记的请求。损坏 JSON、非法版本结构或不支持的 URL 安全跳过。
4. **源码解析**：逐项处理非空 `sourcesContent`，从源码中提取端点。若对应源码内容缺失或为 `null`，根据 `sourceRoot` 和 map URL 归一化 `sources[]` 路径并请求源码；只允许 HTTP(S) 目标，排除 `data:`、`file:`、依赖目录和 webpack runtime 等不可用来源。取回的源码响应按 Source Map 来源处理，不依赖 `.js` 后缀或 JavaScript MIME 类型。
5. **候选合并**：保留 `Source`、`Tag`、`Attribute` 来源信息；同一规范化 URL 与方法只入队一次。源码文本里的相对端点以相应源文件 URL 为解析基准；无法安全确定基准的候选跳过。

解析器应使用纯函数完成引用识别、map 解码、source 路径归一化和源码端点提取；爬虫层只负责请求调度和响应来源分派。无需引入新的 Source Map 依赖，可使用标准库 JSON 解码并复用现有 JS 端点提取实现。

## 接口与资源边界

- 在 `types.Options`、`parser.Options` 与 `NewCrawlerOptions` 之间传递 `ScrapeJSMapResponses`；CLI 增加 `-jsmap` 帮助文案。为 Windows/386 的 `parser_nojs.go` 增加对应的 regex Source Map 路径，确保行为一致且不依赖 jsluice 构建标签。
- 新增 parser 处理器时保持 `ParseResponse` 的现有注册模式；普通 JS 提取器重构为可供独立源码字符串调用的 helper，避免构造伪响应或复制提取逻辑。
- 沿用 katana 的最大响应大小、scope、depth 和 crawl duration 限制；另设置 Source Map 专属上限：递归深度 8、每个 map 最多处理 256 个 sections、每个 map 最多产出 2,000 个候选。超限后停止该 map 的后续解析，不影响其他响应。
- 不解码 `mappings` 字段；端点来自 `sourcesContent` 或成功请求到的源码正文。

## 实施参考与验收

参考 [basecrawler 的纯 Source Map 解析器](C:/ISEC/Deving/crawlers/basecrawler/parse/sourcemap.go:18) 和 [响应调度实现](C:/ISEC/Deving/crawlers/basecrawler/internal/crawler/crawler.go:646)，借鉴结构、边界保护与测试思路，不直接复制其请求类型或编排代码。katana 侧主要涉及 `pkg/engine/parser`、`pkg/types`、`pkg/utils/extensions` 及 CLI 参数注册与 README。

针对性测试至少覆盖：

- `-jsmap` 单独运行可发现 JS 中的相对、根相对和绝对 map 引用；`-jc`、`-jsl` 各自及组合启用时行为符合上述开关语义。
- 普通 v3、内嵌 indexed sections、外部 sections 的递归解析；`sourcesContent` 中的端点与缺失内容时回源取得的 JS/TS 源码端点均可入队。
- 根路径、`sourceRoot`、HTTP(S)、webpack 风格 source 路径的归一化；非法 scheme、不可解析相对路径、`node_modules` 与 runtime 源不产生请求。
- 默认 `.map` 过滤仅对 Source Map 来源请求开例外；显式扩展过滤、scope、去重仍生效。
- 重复声明、重复 sources、循环/超深 sections、超大候选列表、畸形 JSON、HTTP 错误响应和超响应大小场景安全处理。
- `go test` 目标 parser/types/extensions 包，并编译 Windows/386 parser 构建分支；端到端测试确认 map 与源文件请求进入现有队列并能输出源码中发现的端点。

//go:build js && wasm

// Command osb-wasm is the WebAssembly build used by the website. It defines
// the global object `osb` with:
//
//	osb.render(requestJSON) -> responseJSON   (see internal/web.Request/Response)
//	osb.catalog()           -> catalogJSON    (actions, flavors, targets, schema)
//	osb.version             -> string
package main

import (
	"encoding/json"
	"syscall/js"

	"github.com/SvenTo/osb/internal/web"
)

var version = "dev"

func main() {
	web.Version = version
	api := js.Global().Get("Object").New()
	api.Set("version", version)
	api.Set("render", js.FuncOf(func(_ js.Value, args []js.Value) any {
		var req web.Request
		if len(args) > 0 {
			if err := json.Unmarshal([]byte(args[0].String()), &req); err != nil {
				return marshal(web.Response{Error: "bad request: " + err.Error()})
			}
		}
		return marshal(web.Render(req))
	}))
	var catalog string
	api.Set("catalog", js.FuncOf(func(js.Value, []js.Value) any {
		if catalog == "" {
			catalog = marshal(web.GetCatalog())
		}
		return catalog
	}))
	js.Global().Set("osb", api)
	if cb := js.Global().Get("osbReady"); cb.Type() == js.TypeFunction {
		cb.Invoke()
	}
	select {}
}

func marshal(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return `{"error":"internal error: ` + err.Error() + `"}`
	}
	return string(b)
}

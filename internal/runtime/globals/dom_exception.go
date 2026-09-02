package globals

import (
	"github.com/aluka-lang/aluka/internal/engine"
)

// DOMExceptionConfig configures the global DOMException constructor.
type DOMExceptionConfig struct{}

// NewDOMException registers the Web API DOMException constructor.
func NewDOMException(ctx engine.Context, cfg DOMExceptionConfig) error {
	proto := engine.NewObject()
	if errorCtor, err := ctx.Global().Get("Error"); err == nil {
		if errorObj, ok := errorCtor.AsObject(); ok {
			if errorProto, err := errorObj.Get("prototype"); err == nil {
				if errorProtoObj, ok := errorProto.AsObject(); ok {
					engine.SetProto(proto, errorProtoObj)
				}
			}
		}
	}
	_ = proto.Set("name", engine.Str("Error"))
	_ = proto.Set("message", engine.Str(""))
	_ = proto.Set("code", engine.IntValue(0))

	ctor := engine.NewFunction("DOMException", func(args []engine.Value) (engine.Value, error) {
		message := ""
		name := "Error"
		if len(args) > 0 && !args[0].IsUndefined() {
			message = args[0].String()
		}
		if len(args) > 1 && !args[1].IsUndefined() {
			name = args[1].String()
		}

		instance := engine.NewObject()
		engine.SetProto(instance, proto)
		_ = instance.Set("message", engine.Str(message))
		_ = instance.Set("name", engine.Str(name))
		_ = instance.Set("code", engine.IntValue(domExceptionCode(name)))
		return instance, nil
	})
	ctorObj, _ := ctor.AsObject()
	_ = ctorObj.Set("prototype", proto)
	_ = proto.Set("constructor", ctor)
	return ctx.Global().Set("DOMException", ctor)
}

func domExceptionCode(name string) int {
	switch name {
	case "IndexSizeError":
		return 1
	case "HierarchyRequestError":
		return 3
	case "WrongDocumentError":
		return 4
	case "InvalidCharacterError":
		return 5
	case "NoModificationAllowedError":
		return 7
	case "NotFoundError":
		return 8
	case "NotSupportedError":
		return 9
	case "InUseAttributeError":
		return 10
	case "InvalidStateError":
		return 11
	case "SyntaxError":
		return 12
	case "InvalidModificationError":
		return 13
	case "NamespaceError":
		return 14
	case "InvalidAccessError":
		return 15
	case "TypeMismatchError":
		return 17
	case "SecurityError":
		return 18
	case "NetworkError":
		return 19
	case "AbortError":
		return 20
	case "URLMismatchError":
		return 21
	case "QuotaExceededError":
		return 22
	case "TimeoutError":
		return 23
	case "InvalidNodeTypeError":
		return 24
	case "DataCloneError":
		return 25
	default:
		return 0
	}
}

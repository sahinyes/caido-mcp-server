package tools

import "fmt"

// buildTamperSectionMap constructs the section input as a map.
// Every nested type in the tamper section chain is a GraphQL oneof
// (section, operation, matcher, replacer). Using maps ensures only
// the chosen variant is serialized -- no null fields for unset
// variants at any nesting level.
func buildTamperSectionMap(
	section, match, replace string,
) (map[string]any, error) {
	matcher := map[string]any{
		"regex": map[string]any{"regex": match},
	}
	replacer := map[string]any{
		"term": map[string]any{"term": replace},
	}
	rawOp := map[string]any{
		"raw": map[string]any{
			"matcher":  matcher,
			"replacer": replacer,
		},
	}

	wrap := func(key string, op map[string]any) map[string]any {
		return map[string]any{
			key: map[string]any{"operation": op},
		}
	}

	switch section {
	case "requestAll":
		return wrap("requestAll", rawOp), nil
	case "requestHeader":
		return wrap("requestHeader", rawOp), nil
	case "requestBody":
		return wrap("requestBody", rawOp), nil
	case "requestPath":
		return wrap("requestPath", rawOp), nil
	case "requestQuery":
		return wrap("requestQuery", rawOp), nil
	case "requestMethod":
		return wrap("requestMethod", map[string]any{
			"update": map[string]any{
				"matcher":  matcher,
				"replacer": replacer,
			},
		}), nil
	case "requestFirstLine":
		return wrap("requestFirstLine", rawOp), nil
	case "requestSNI":
		return wrap("requestSNI", rawOp), nil
	case "responseAll":
		return wrap("responseAll", rawOp), nil
	case "responseHeader":
		return wrap("responseHeader", rawOp), nil
	case "responseBody":
		return wrap("responseBody", rawOp), nil
	case "responseFirstLine":
		return wrap("responseFirstLine", rawOp), nil
	case "responseStatusCode":
		return wrap("responseStatusCode", map[string]any{
			"update": map[string]any{
				"matcher":  matcher,
				"replacer": replacer,
			},
		}), nil
	default:
		return nil, fmt.Errorf(
			"unknown section %q: use requestAll, requestHeader, "+
				"requestBody, responseAll, responseHeader, responseBody, "+
				"or other supported sections", section,
		)
	}
}

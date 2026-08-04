package localruntime

import "testing"

func TestDecodeLanguageConfigIncludesOutputRoutes(t *testing.T) {
	pairs, routes, err := decodeLanguageConfig([]byte(`{
        "language_pairs": [{"source":"zh-CN","target":"en-US"}],
        "output_routes": [{"target_language":"en-US","tts_enabled":false,"delivery_enabled":true}]
    }`))
	if err != nil {
		t.Fatalf("decodeLanguageConfig() error = %v", err)
	}
	if len(pairs) != 1 || pairs[0].Source != "zh-CN" || pairs[0].Target != "en-US" {
		t.Fatalf("pairs = %#v", pairs)
	}
	if len(routes) != 1 || routes[0].TargetLanguage != "en-US" || routes[0].TTSEnabled || !routes[0].DeliveryEnabled {
		t.Fatalf("routes = %#v", routes)
	}
}

func TestDecodeLanguageConfigRetainsLegacyPairArray(t *testing.T) {
	pairs, routes, err := decodeLanguageConfig([]byte(`[{"source":"zh-CN","target":"en-US"}]`))
	if err != nil {
		t.Fatalf("decodeLanguageConfig() error = %v", err)
	}
	if len(pairs) != 1 || len(routes) != 0 {
		t.Fatalf("pairs=%#v routes=%#v", pairs, routes)
	}
}

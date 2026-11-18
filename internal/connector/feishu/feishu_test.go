package feishu

import "testing"

func TestRenderContent(t *testing.T) {
	cases := []struct{ typ, raw, wantType, want string }{
		{"text", `{"text":"@_user_1 你好"}`, "text", "@_user_1 你好"},
		{"post", `{"title":"周报","content":[[{"tag":"text","text":"本周完成"},{"tag":"at","user_name":"张三"}]]}`, "text", "周报\n本周完成 张三"},
		{"image", `{"image_key":"img_x"}`, "image", "[图片]"},
		{"file", `{"file_name":"a.pdf"}`, "file", "[文件] a.pdf"},
		{"interactive", `{"title":"告警: CPU","elements":[[{"tag":"text","text":"host-1 95%"}]]}`, "card", "告警: CPU host-1 95%"},
	}
	for _, c := range cases {
		gt, g := renderContent(c.typ, c.raw)
		if gt != c.wantType || g != c.want {
			t.Errorf("%s: got (%s,%q) want (%s,%q)", c.typ, gt, g, c.wantType, c.want)
		}
	}
}

package main

import (
	"errors"
	"fmt"
	"html/template"
	"os"
)

func writeHTML(path string, r *report) error {
	t, err := template.New("report").Funcs(template.FuncMap{
		"f3":     func(n float64) string { return fmt.Sprintf("%.3f", n) },
		"f6":     func(n float64) string { return fmt.Sprintf("%.6f", n) },
		"frames": func() []int { return []int{0, r.Config.Frames / 2, r.Config.Frames - 1} },
		"image": func(name string, frame int, crop, suffix string) string {
			if crop != "" {
				crop = "-" + crop
			}
			return fmt.Sprintf("review/%s-frame-%04d%s-%s.png", name, frame, crop, suffix)
		},
	}).Parse(reportHTML)
	if err != nil {
		return err
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	err = t.Execute(f, r)
	return errors.Join(err, f.Close())
}

const reportHTML = `<!doctype html>
<html lang="en"><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>Aircast decoded image measurements</title>
<style>
body{font:16px/1.5 system-ui,sans-serif;margin:32px auto;padding:0 24px;max-width:1500px;color:#17212b;background:#f6f7f8}h1{font-size:30px}h2{margin-top:40px}h3{margin-top:24px}table{border-collapse:collapse;background:white;font-variant-numeric:tabular-nums}td,th{border:1px solid #bdc6ce;padding:8px 12px;text-align:left;vertical-align:top}.scroll{overflow:auto}.notice{border-left:4px solid #bc6c00;padding:12px 20px;background:#fff7e5}.error{color:#a20d16;white-space:pre-wrap}.pair{display:flex;gap:20px;overflow:auto;align-items:start}.pair figure{margin:8px 0;flex:none}.pair img{image-rendering:pixelated;display:block}.preview{width:440px;height:auto}figcaption{font-size:14px}code,pre{font:13px/1.5 ui-monospace,monospace}pre{white-space:pre-wrap;overflow-wrap:anywhere}a{color:#064d86}details{margin:16px 0}summary{cursor:pointer;font-weight:600}.metric{font-variant-numeric:tabular-nums}
</style>
<h1>Aircast decoded image measurements</h1>
<div class="notice">Status: <strong>{{.Status}}</strong>. These are synthetic local encode/decode measurements, not capture, transport, hardware decoder, or physical TV validation. No perceptual passing threshold is applied.</div>
{{if .Error}}<h2>Failure</h2><pre class="error">{{.Error}}</pre>{{end}}
<p>Source {{.Config.Width}}×{{.Config.Height}}. Encoded {{.Config.TargetWidth}}×{{.Config.TargetHeight}}. {{.Config.Frames}} frames at {{.Config.FPS}} fps. {{.Started}}.</p>
<p>{{.FFmpegVersion}}. <a href="results.json">Complete JSON, commands, hashes and per-frame errors</a>.</p>
<h2>Trial settings</h2>
<table><tr><th>Candidate</th><th>Preset</th><th>CRF</th><th>Maximum rate</th><th>VBV bits / milliseconds</th></tr>
{{range .Profiles}}<tr><td>{{.Name}}</td><td>{{.Preset}}</td><td>{{.CRF}}</td><td>{{.MaxRate}}</td><td>{{.Buffer}} / {{.CapacityMS}}</td></tr>{{end}}</table>
<p>H.264 High 4.0, 8-bit 4:2:0, BT.709 limited range, left chroma siting, square pixels. Zero B-frames and lookahead, two-second maximum GOP, two codec threads. Receiver acceptance remains untested.</p>
<h2>Loss stages</h2>
<div class="scroll"><table><tr><th>Comparison</th><th>RGB MAE / RMSE</th><th>BT.709 luma MAE / RMSE</th><th>FFmpeg PSNR dB</th><th>FFmpeg SSIM</th><th>Metric domain</th></tr>
{{range .Comparisons}}<tr><td><a href="#{{.Name}}">{{.Name}}</a></td><td>{{f3 .Errors.RGBMAE}} / {{f3 .Errors.RGBRMSE}}</td><td>{{f3 .Errors.LumaMAE}} / {{f3 .Errors.LumaRMSE}}</td><td>{{.FFmpeg.PSNRDB}}</td><td>{{f6 .FFmpeg.SSIM}}</td><td>{{.FFmpeg.Domain}}</td></tr>{{end}}</table></div>
<p>RGB/luma errors use decoded full-range RGB code values, not linear-light error. Native codec PSNR/SSIM use Y/U/V; other rows use R/G/B. Do not rank rows across those domains or add stage PSNR values. Full-frame scores include borders and the frame marker; fixed text crops do not.</p>
<h2>Independent pre-encode matrix/range oracle</h2>
<table><tr><th>Patch</th><th>Expected Y/Cb/Cr</th><th>Actual</th></tr>{{range .YUVOracle}}<tr><td>{{.Name}}</td><td>{{.Expected}}</td><td>{{.Actual}}</td></tr>{{end}}</table>
<p>Flat-patch interiors must match the independent BT.709 limited-range equation within one rounding code. This is a conversion check, not an image-quality threshold. Fixture RGB values already represent nonlinear BT.709; sRGB-to-BT.709 transfer conversion is not tested.</p>
{{range .Comparisons}}{{$comparison := .}}
<section id="{{.Name}}"><h2>{{.Name}}</h2><p>{{.Meaning}}</p>
<p><a href="{{.Reference}}">Reference video</a> · <a href="{{.Candidate}}">Candidate video</a> · <a href="{{.FFmpeg.PSNRLog}}">Per-frame PSNR</a> · <a href="{{.FFmpeg.SSIMLog}}">Per-frame SSIM</a></p>
<pre>{{.FFmpeg.PSNRSummary}}
{{.FFmpeg.SSIMSummary}}</pre>
<div class="scroll"><table><tr><th>ROI</th><th>RGB MAE / RMSE / max</th><th>Luma MAE / RMSE / max</th><th>PSNR dB</th><th>SSIM</th></tr>
{{range .Crops}}<tr><td>{{.Name}} {{.Bounds}}</td><td>{{f3 .Errors.RGBMAE}} / {{f3 .Errors.RGBRMSE}} / {{.Errors.RGBMax}}</td><td>{{f3 .Errors.LumaMAE}} / {{f3 .Errors.LumaRMSE}} / {{f3 .Errors.LumaMax}}</td><td>{{if .FFmpeg.Domain}}<a href="{{.FFmpeg.PSNRLog}}">{{.FFmpeg.PSNRDB}}</a>{{else}}not measured{{end}}</td><td>{{if .FFmpeg.Domain}}<a href="{{.FFmpeg.SSIMLog}}">{{f6 .FFmpeg.SSIM}}</a>{{else}}not measured{{end}}</td></tr>{{end}}</table></div>
{{range frames}}{{$frame := .}}<details><summary>Frame {{.}}. Native crops and pixel differences</summary>
<p>Full-frame previews are resized here. Open a PNG for 1:1 inspection. Crops below retain native pixels; horizontal scrolling does not resize them. Difference PNGs show absolute RGB error multiplied by four and clipped at 255.</p>
<div class="pair"><figure><figcaption>Reference</figcaption><a href="{{image $comparison.Name . "" "reference"}}"><img class="preview" src="{{image $comparison.Name . "" "reference"}}" alt="Full reference frame" loading="lazy"></a></figure><figure><figcaption>Decoded</figcaption><a href="{{image $comparison.Name . "" "decoded"}}"><img class="preview" src="{{image $comparison.Name . "" "decoded"}}" alt="Full decoded frame" loading="lazy"></a></figure></div>
{{range $comparison.Crops}}{{if or (eq .Name "black-text") (eq .Name "colored-text") (eq .Name "one-pixel") (eq .Name "aspect")}}<h3>{{.Name}}</h3><div class="pair">
<figure><figcaption>Reference</figcaption><img src="{{image $comparison.Name $frame .Name "reference"}}" alt="Reference crop" loading="lazy"></figure>
<figure><figcaption>Decoded</figcaption><img src="{{image $comparison.Name $frame .Name "decoded"}}" alt="Decoded crop" loading="lazy"></figure>
<figure><figcaption>Absolute difference ×4</figcaption><img src="{{image $comparison.Name $frame .Name "diff-x4"}}" alt="Amplified difference crop" loading="lazy"></figure>
</div>{{end}}{{end}}</details>{{end}}</section>{{end}}
<h2>Command receipts</h2>
{{range .Commands}}<details><summary>{{.Log}} · {{f3 .Seconds}} seconds {{if .Error}}· FAILED{{end}}</summary><pre>{{range .Args}}{{printf "%q" .}} {{end}}</pre>{{if .Error}}<p class="error">{{.Error}}</p>{{end}}<a href="{{.Log}}">Full command output</a></details>{{end}}
</html>`

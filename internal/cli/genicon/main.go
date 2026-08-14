package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
)

// 创建高品质平滑渐变与多面体抗锯齿渲染
func renderAlukaIcon(size int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	scale := float64(size) / 256.0

	// 1. 深色微发光背景圆角矩形
	cx := float64(size) / 2.0
	cy := float64(size) / 2.0
	r := float64(size) * 0.44
	corner := float64(size) * 0.22

	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			fx := float64(x) + 0.5
			fy := float64(y) + 0.5

			// 计算圆角矩形距离
			dx := math.Max(0, math.Abs(fx-cx)-(r-corner))
			dy := math.Max(0, math.Abs(fy-cy)-(r-corner))
			dist := math.Sqrt(dx*dx + dy*dy)

			if dist <= corner {
				// 背景径向渐变 (#161F36 到 #07090F)
				distFromCenter := math.Hypot(fx-cx, fy-(cy-20*scale)) / (r * 1.3)
				distFromCenter = math.Min(1.0, distFromCenter)

				// 顶部微青色微光
				rCol := uint8(22*(1-distFromCenter) + 6*distFromCenter)
				gCol := uint8(31*(1-distFromCenter) + 9*distFromCenter)
				bCol := uint8(54*(1-distFromCenter) + 16*distFromCenter)

				// 边缘平滑抗锯齿
				alpha := 1.0
				if dist > corner-1.0 {
					alpha = corner - dist
				}

				// 细边框高光
				if dist > corner-2.5 {
					// 青粉渐变边框
					t := (fx + fy) / float64(size*2)
					brR := uint8(0*(1-t) + 255*t)
					brG := uint8(240*(1-t) + 0*t)
					brB := uint8(255*(1-t) + 122*t)
					rCol = brR
					gCol = brG
					bCol = brB
				}

				img.SetRGBA(x, y, color.RGBA{
					R: uint8(float64(rCol) * alpha),
					G: uint8(float64(gCol) * alpha),
					B: uint8(float64(bCol) * alpha),
					A: uint8(255 * alpha),
				})
			}
		}
	}

	// 2. 绘制多面体晶体 "A"（左翼青色、右翼品红、中顶水晶白、中间横梁）
	drawFacet := func(pts [][2]float64, c1, c2 color.RGBA, isVertical bool) {
		// 采样多重抗锯齿 (4x4 SSAA)
		minX, minY, maxX, maxY := size, size, 0, 0
		for _, p := range pts {
			px := int(p[0] * scale)
			py := int(p[1] * scale)
			if px < minX {
				minX = px
			}
			if px > maxX {
				maxX = px
			}
			if py < minY {
				minY = py
			}
			if py > maxY {
				maxY = py
			}
		}
		minX = int(math.Max(0, float64(minX-1)))
		minY = int(math.Max(0, float64(minY-1)))
		maxX = int(math.Min(float64(size-1), float64(maxX+1)))
		maxY = int(math.Min(float64(size-1), float64(maxY+1)))

		for y := minY; y <= maxY; y++ {
			for x := minX; x <= maxX; x++ {
				hits := 0
				const sub = 4
				for sy := 0; sy < sub; sy++ {
					for sx := 0; sx < sub; sx++ {
						px := (float64(x) + (float64(sx)+0.5)/sub) / scale
						py := (float64(y) + (float64(sy)+0.5)/sub) / scale
						if pointInPoly(px, py, pts) {
							hits++
						}
					}
				}
				if hits > 0 {
					cov := float64(hits) / float64(sub*sub)
					t := 0.0
					if isVertical {
						t = (float64(y)/scale - pts[0][1]) / (pts[len(pts)-1][1] - pts[0][1] + 0.001)
					} else {
						t = (float64(x)/scale - pts[0][0]) / (pts[len(pts)-1][0] - pts[0][0] + 0.001)
					}
					t = math.Max(0, math.Min(1, t))

					r := uint8(float64(c1.R)*(1-t) + float64(c2.R)*t)
					g := uint8(float64(c1.G)*(1-t) + float64(c2.G)*t)
					b := uint8(float64(c1.B)*(1-t) + float64(c2.B)*t)

					// Alpha 混合
					old := img.RGBAAt(x, y)
					dstA := float64(old.A) / 255.0
					srcA := cov
					outA := srcA + dstA*(1-srcA)

					if outA > 0 {
						outR := (float64(r)*srcA + float64(old.R)*dstA*(1-srcA)) / outA
						outG := (float64(g)*srcA + float64(old.G)*dstA*(1-srcA)) / outA
						outB := (float64(b)*srcA + float64(old.B)*dstA*(1-srcA)) / outA
						img.SetRGBA(x, y, color.RGBA{
							R: uint8(outR),
							G: uint8(outG),
							B: uint8(outB),
							A: uint8(outA * 255),
						})
					}
				}
			}
		}
	}

	// 晶体左翼（赛博青 #00F0FF -> #0055FF）
	drawFacet([][2]float64{
		{128, 40}, {128, 98}, {64, 192}, {38, 192},
	}, color.RGBA{0, 240, 255, 255}, color.RGBA{0, 85, 255, 255}, true)

	// 晶体右翼（极光品红 #FF007A -> #4A00E0）
	drawFacet([][2]float64{
		{128, 40}, {128, 98}, {192, 192}, {218, 192},
	}, color.RGBA{255, 0, 122, 255}, color.RGBA{74, 0, 224, 255}, true)

	// 顶点发光多面晶体
	drawFacet([][2]float64{
		{128, 40}, {102, 98}, {154, 98},
	}, color.RGBA{255, 255, 255, 255}, color.RGBA{0, 180, 216, 255}, true)

	// 内部反转三角镂空 (#0B0E17)
	drawFacet([][2]float64{
		{128, 114}, {94, 166}, {162, 166},
	}, color.RGBA{11, 14, 23, 255}, color.RGBA{11, 14, 23, 255}, true)

	// 横梁能量条（#00F0FF -> #FF007A）
	drawFacet([][2]float64{
		{80, 148}, {176, 148}, {166, 166}, {90, 166},
	}, color.RGBA{0, 240, 255, 255}, color.RGBA{255, 0, 122, 255}, false)

	return img
}

func pointInPoly(x, y float64, pts [][2]float64) bool {
	inside := false
	n := len(pts)
	j := n - 1
	for i := 0; i < n; i++ {
		xi, yi := pts[i][0], pts[i][1]
		xj, yj := pts[j][0], pts[j][1]
		intersect := ((yi > y) != (yj > y)) && (x < (xj-xi)*(y-yi)/(yj-yi+1e-9)+xi)
		if intersect {
			inside = !inside
		}
		j = i
	}
	return inside
}

// 编码标准 Windows .ico 格式
func encodeICO(images []*image.RGBA) ([]byte, error) {
	var pngBuffers [][]byte
	for _, img := range images {
		var buf bytes.Buffer
		if err := png.Encode(&buf, img); err != nil {
			return nil, err
		}
		pngBuffers = append(pngBuffers, buf.Bytes())
	}

	var ico bytes.Buffer
	// ICONDIR
	binary.Write(&ico, binary.LittleEndian, uint16(0)) // Reserved
	binary.Write(&ico, binary.LittleEndian, uint16(1)) // Type 1 = ICO
	binary.Write(&ico, binary.LittleEndian, uint16(len(images)))

	// 计算头部 + 所有 Entry 的总偏移
	offset := 6 + len(images)*16

	for i, img := range images {
		w := img.Bounds().Dx()
		h := img.Bounds().Dy()
		pngSize := uint32(len(pngBuffers[i]))

		bw := byte(w)
		if w >= 256 {
			bw = 0
		}
		bh := byte(h)
		if h >= 256 {
			bh = 0
		}

		ico.WriteByte(bw)
		ico.WriteByte(bh)
		ico.WriteByte(0) // Colors
		ico.WriteByte(0) // Reserved
		binary.Write(&ico, binary.LittleEndian, uint16(1))
		binary.Write(&ico, binary.LittleEndian, uint16(32))
		binary.Write(&ico, binary.LittleEndian, pngSize)
		binary.Write(&ico, binary.LittleEndian, uint32(offset))

		offset += int(pngSize)
	}

	for _, p := range pngBuffers {
		ico.Write(p)
	}

	return ico.Bytes(), nil
}

func main() {
	sizes := []int{256, 128, 64, 48, 32, 16}
	var renderedImgs []*image.RGBA

	for _, s := range sizes {
		img := renderAlukaIcon(s)
		renderedImgs = append(renderedImgs, img)
	}

	// 1. 写 assets/icon.png (256x256)
	_ = os.MkdirAll("assets", 0755)
	pngFile, err := os.Create("assets/icon.png")
	if err != nil {
		panic(err)
	}
	defer pngFile.Close()
	if err := png.Encode(pngFile, renderedImgs[0]); err != nil {
		panic(err)
	}

	// 2. 写 assets/icon.ico (全尺寸打包)
	icoData, err := encodeICO(renderedImgs)
	if err != nil {
		panic(err)
	}
	if err := os.WriteFile("assets/icon.ico", icoData, 0644); err != nil {
		panic(err)
	}

	fmt.Println("Generated assets/icon.png and assets/icon.ico successfully!")
}

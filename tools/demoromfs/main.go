// Command demoromfs writes a small, entirely synthetic RomFS for demos and
// screenshots. Every cue name is invented and every sound is synthesized here
// (tones, chords and filtered noise); none of it comes from any game.
//
//	go run ./tools/demoromfs -out ./demo-romfs
//	barsdig serve ./demo-romfs
package main

import (
	"encoding/binary"
	"flag"
	"fmt"
	"math"
	"math/rand"
	"os"
	"path/filepath"

	"barsdig/internal/synth"
)

const rate = 48000

const (
	codecPCM16 = 0
	codecDSP   = 1
)

// sound is the audio and metadata for one cue before encoding.
type sound struct {
	name    string
	ch      [][]float64 // per channel, -1..1
	codec   uint16
	loop    *[2]int32
	vol     float32
	markers []synth.Marker
	stream  bool // store a prefetch stub in the bank and the full file in Stream/
}

func main() {
	out := flag.String("out", "demo-romfs", "directory to create")
	flag.Parse()
	r := rand.New(rand.NewSource(7))

	banks := []struct {
		dir, name string
		cues      []sound
	}{
		{"Sound/Resource", "Music", music(r)},
		{"Sound/Resource", "Ambience", ambience(r)},
		{"Sound/Resource", "UI", ui()},
		{"Sound/Resource", "Player", player(r)},
		{"Sound/Resource", "Creature", creature(r)},
		{"Voice/Resource", "Narrator", narrator(r)},
	}
	for _, b := range banks {
		var entries []synth.Entry
		for _, s := range b.cues {
			full := encode(s, false)
			e := synth.Entry{Name: s.name, VolumeDB: s.vol, Channels: byte(len(s.ch)), Markers: s.markers, BWAV: full}
			if s.stream {
				e.BWAV = encode(s, true)
				write(filepath.Join(*out, b.dir, "Stream", s.name+".bwav"), full)
			}
			entries = append(entries, e)
		}
		if b.name == "UI" { // one entry sharing another's audio, one with none
			entries = append(entries,
				synth.Entry{Name: "SE_Confirm_Alt", Channels: 1, AliasOf: 4},
				synth.Entry{Name: "SE_Reserved", Channels: 1})
		}
		write(filepath.Join(*out, b.dir, b.name+".bars"), synth.BARS(entries))
	}
	fmt.Println("wrote", *out)
}

func write(p string, d []byte) {
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		panic(err)
	}
	if err := os.WriteFile(p, d, 0o644); err != nil {
		panic(err)
	}
}

// ---- encoding ----

var dspCoefs = [16]int16{0, 0, 2048, 0, 3600, -1600, 3072, -1024, 1536, 0, 4000, -2000, 3800, -1800, 2560, -512}

func toPCM(x []float64) []int16 {
	o := make([]int16, len(x))
	for i, v := range x {
		o[i] = int16(math.Max(-1, math.Min(1, v)) * 32000)
	}
	return o
}

// encode builds a BWAV; prefetch keeps only the first second.
func encode(s sound, prefetch bool) []byte {
	full := len(s.ch[0])
	n := full
	if prefetch {
		n = min(full, rate)
	}
	spec := synth.BWAVSpec{Codec: s.codec, SampleRate: rate, FullSamples: uint32(full), Samples: uint32(n), Prefetch: prefetch, Loop: s.loop}
	for _, c := range s.ch {
		pcm := toPCM(c)[:n]
		if s.codec == codecPCM16 {
			b := make([]byte, 2*len(pcm))
			for i, v := range pcm {
				binary.LittleEndian.PutUint16(b[2*i:], uint16(v))
			}
			spec.Channels = append(spec.Channels, b)
		} else {
			spec.Channels = append(spec.Channels, dspEncode(pcm))
			spec.Coefs = append(spec.Coefs, dspCoefs)
			spec.Hist = append(spec.Hist, [3]int16{})
		}
	}
	return synth.BWAV(spec)
}

// dspEncode is a simple brute-force DSP-ADPCM encoder: for each 14-sample
// frame it tries every predictor and scale and keeps the least error.
func dspEncode(pcm []int16) []byte {
	var out []byte
	var h1, h2 int32
	for f := 0; f < len(pcm); f += 14 {
		frame := pcm[f:min(f+14, len(pcm))]
		var best []byte
		bestErr, bh1, bh2 := int64(math.MaxInt64), int32(0), int32(0)
		for p := 0; p < 8; p++ {
			c1, c2 := int32(dspCoefs[2*p]), int32(dspCoefs[2*p+1])
			for sc := 0; sc < 12; sc++ {
				buf := make([]byte, 8)
				buf[0] = byte(p<<4 | sc)
				a1, a2 := h1, h2
				var e int64
				for k, want := range frame {
					pred := (1024 + c1*a1 + c2*a2) >> 11
					nib := int32(math.Round(float64(int32(want)-pred) / float64(int32(1)<<sc)))
					nib = max(-8, min(7, nib))
					s := clamp(((nib<<sc)<<11 + 1024 + c1*a1 + c2*a2) >> 11)
					diff := int64(s) - int64(want)
					e += diff * diff
					a2, a1 = a1, s
					if k%2 == 0 {
						buf[1+k/2] |= byte(nib&0xF) << 4
					} else {
						buf[1+k/2] |= byte(nib & 0xF)
					}
				}
				if e < bestErr {
					bestErr, best, bh1, bh2 = e, buf, a1, a2
				}
			}
		}
		out = append(out, best...)
		h1, h2 = bh1, bh2
	}
	return out
}

func clamp(v int32) int32 { return max(-32768, min(32767, v)) }

// ---- synthesis ----

func secs(s float64) int { return int(s * rate) }

func midi(n float64) float64 { return 440 * math.Pow(2, (n-69)/12) }

// tone adds a decaying harmonic tone into x starting at t0.
func tone(x []float64, t0 int, dur float64, freq, amp float64, harm int, decay float64) {
	n := secs(dur)
	for i := 0; i < n && t0+i < len(x); i++ {
		t := float64(i) / rate
		env := math.Min(1, t*200) * math.Exp(-t*decay) * math.Min(1, float64(n-i)/(0.02*rate))
		var v float64
		for h := 1; h <= harm; h++ {
			v += math.Sin(2*math.Pi*freq*float64(h)*t) / float64(h*h)
		}
		x[t0+i] += amp * env * v
	}
}

// noise adds low-passed noise with an envelope function.
func noise(r *rand.Rand, x []float64, t0, n int, cutoff float64, env func(t float64) float64) {
	a := 1 - math.Exp(-2*math.Pi*cutoff/rate)
	var y float64
	for i := 0; i < n && t0+i < len(x); i++ {
		y += a * (r.Float64()*2 - 1 - y)
		x[t0+i] += y * env(float64(i)/rate)
	}
}

func normalize(peak float64, chs ...[]float64) {
	var m float64
	for _, c := range chs {
		for _, v := range c {
			m = math.Max(m, math.Abs(v))
		}
	}
	if m == 0 {
		return
	}
	for _, c := range chs {
		for i := range c {
			c[i] *= peak / m
		}
	}
}

// song renders a looping stereo piece: an intro bar, then a loop body.
func song(r *rand.Rand, name string, root float64, minor bool, bpm float64, bars int, stream bool) sound {
	beat := 60 / bpm
	barLen := 4 * beat
	introBars := 2
	total := secs(barLen * float64(introBars+bars))
	L, R := make([]float64, total), make([]float64, total)
	scale := []float64{0, 2, 4, 5, 7, 9, 11}
	prog := [][]float64{{0, 4, 7}, {5, 9, 12}, {7, 11, 14}, {0, 4, 7}}
	if minor {
		scale = []float64{0, 2, 3, 5, 7, 8, 10}
		prog = [][]float64{{0, 3, 7}, {8, 12, 15}, {3, 7, 10}, {7, 10, 14}}
	}
	for b := 0; b < introBars+bars; b++ {
		t0 := secs(barLen * float64(b))
		ch := prog[b%len(prog)]
		for i, n := range ch { // pad chord, spread across stereo
			f := midi(root + n)
			tone(L, t0, barLen, f, 0.16*float64(3-i)/3+0.05, 4, 0.6)
			tone(R, t0, barLen, f*1.003, 0.16*float64(i+1)/3+0.05, 4, 0.6)
		}
		if b < introBars {
			continue
		}
		for q := 0; q < 4; q++ { // bass and percussion
			tq := t0 + secs(beat*float64(q))
			bf := midi(root - 24 + ch[0])
			tone(L, tq, beat*0.9, bf, 0.35, 3, 3)
			tone(R, tq, beat*0.9, bf, 0.35, 3, 3)
			hat := func(t float64) float64 { return 0.25 * math.Exp(-t*40) }
			noise(r, L, tq+secs(beat/2), secs(0.1), 9000, hat)
			noise(r, R, tq+secs(beat/2), secs(0.1), 9000, hat)
			if q%2 == 0 {
				kick := func(t float64) float64 { return 0.9 * math.Exp(-t*18) }
				noise(r, L, tq, secs(0.2), 180, kick)
				noise(r, R, tq, secs(0.2), 180, kick)
			}
		}
		for e := 0; e < 8; e++ { // melody, eighth notes
			if r.Float64() < 0.25 {
				continue
			}
			deg := scale[r.Intn(len(scale))] + 12
			te := t0 + secs(beat/2*float64(e))
			pan := r.Float64()
			tone(L, te, beat*0.6, midi(root+deg), 0.3*(1-pan)+0.1, 6, 4)
			tone(R, te, beat*0.6, midi(root+deg), 0.3*pan+0.1, 6, 4)
		}
	}
	normalize(0.85, L, R)
	ls := int32(secs(barLen * float64(introBars)))
	return sound{
		name: name, ch: [][]float64{L, R}, codec: codecDSP, loop: &[2]int32{ls, int32(total - 1)}, vol: -3,
		markers: []synth.Marker{{ID: 0, Start: 0, Length: uint32(ls), Name: "Intro"}, {ID: 1, Start: uint32(ls), Length: uint32(total) - uint32(ls), Name: "Loop"}},
		stream:  stream,
	}
}

func music(r *rand.Rand) []sound {
	return []sound{
		song(r, "BGM_Title", 60, false, 92, 12, true),
		song(r, "BGM_Field_Day", 62, false, 118, 16, true),
		song(r, "BGM_Field_Night", 57, true, 84, 10, true),
		song(r, "BGM_Village", 65, false, 104, 8, false),
		song(r, "BGM_Cave", 52, true, 70, 6, false),
		song(r, "BGM_Battle", 55, true, 150, 16, true),
		song(r, "BGM_Shop", 67, false, 126, 6, false),
		song(r, "BGM_Credits", 60, false, 96, 28, true),
	}
}

func ambience(r *rand.Rand) []sound {
	mk := func(name string, dur, cutoff, wobble float64) sound {
		n := secs(dur)
		L, R := make([]float64, n), make([]float64, n)
		env := func(t float64) float64 { return 0.6 + 0.4*math.Sin(2*math.Pi*wobble*t) }
		noise(r, L, 0, n, cutoff, env)
		noise(r, R, 0, n, cutoff, env)
		normalize(0.5, L, R)
		return sound{name: name, ch: [][]float64{L, R}, codec: codecDSP, loop: &[2]int32{0, int32(n - 1)}, vol: -6}
	}
	birds := mk("AMB_Forest_Birds", 12, 600, 0.1)
	for i := 0; i < 14; i++ {
		t0 := secs(r.Float64() * 11)
		f := 2500 + r.Float64()*1500
		for k := 0; k < 3; k++ {
			tone(birds.ch[i%2], t0+secs(0.09*float64(k)), 0.07, f*(1+0.1*float64(k)), 0.5, 2, 30)
		}
	}
	normalize(0.6, birds.ch...)
	return []sound{
		mk("AMB_Wind_Soft", 10, 400, 0.15),
		mk("AMB_Wind_Strong", 10, 1200, 0.3),
		mk("AMB_Rain_Light", 8, 5000, 0.05),
		mk("AMB_River", 9, 1800, 0.4),
		birds,
	}
}

func mono(name string, n int, codec uint16, fill func(x []float64)) sound {
	x := make([]float64, n)
	fill(x)
	normalize(0.8, x)
	return sound{name: name, ch: [][]float64{x}, codec: codec}
}

func ui() []sound {
	blip := func(name string, notes []float64, step float64) sound {
		return mono(name, secs(step*float64(len(notes))+0.3), codecPCM16, func(x []float64) {
			for i, n := range notes {
				tone(x, secs(step*float64(i)), 0.25, midi(n), 0.6, 3, 14)
			}
		})
	}
	return []sound{
		blip("SE_Menu_Open", []float64{72, 79}, 0.06),
		blip("SE_Menu_Close", []float64{79, 72}, 0.06),
		blip("SE_Cursor_Move", []float64{84}, 0.04),
		blip("SE_Confirm", []float64{76, 83, 88}, 0.05),
		blip("SE_Cancel", []float64{67, 62}, 0.07),
		blip("SE_Item_Get", []float64{72, 76, 79, 84, 88}, 0.09),
		blip("SE_Coin", []float64{88, 95}, 0.05),
		blip("SE_Save_Complete", []float64{67, 72, 76, 79, 84}, 0.12),
		blip("SE_Error", []float64{50, 50}, 0.12),
	}
}

func player(r *rand.Rand) []sound {
	var out []sound
	step := func(name string, cutoff float64) sound {
		return mono(name, secs(0.25), codecDSP, func(x []float64) {
			noise(r, x, 0, len(x), cutoff, func(t float64) float64 { return math.Exp(-t * 30) })
		})
	}
	for i := 1; i <= 3; i++ {
		out = append(out, step(fmt.Sprintf("SE_Footstep_Grass_%02d", i), 2500+300*float64(i)))
	}
	for i := 1; i <= 3; i++ {
		out = append(out, step(fmt.Sprintf("SE_Footstep_Stone_%02d", i), 6000+500*float64(i)))
	}
	swing := func(name string, dur, cutoff float64) sound {
		return mono(name, secs(dur), codecDSP, func(x []float64) {
			noise(r, x, 0, len(x), cutoff, func(t float64) float64 { return math.Sin(math.Pi * t / dur) })
		})
	}
	out = append(out,
		swing("SE_Swing_Light", 0.25, 3000),
		swing("SE_Swing_Heavy", 0.45, 1500),
		mono("SE_Jump", secs(0.3), codecDSP, func(x []float64) {
			for i := range x {
				t := float64(i) / rate
				x[i] = math.Sin(2*math.Pi*(200*t+900*t*t)) * math.Exp(-t*8)
			}
		}),
		step("SE_Land", 400),
	)
	return out
}

func creature(r *rand.Rand) []sound {
	sweep := func(name string, dur, f0, f1, vib float64) sound {
		return mono(name, secs(dur), codecDSP, func(x []float64) {
			var ph float64
			for i := range x {
				t := float64(i) / rate
				f := f0 + (f1-f0)*math.Sin(math.Pi*t/dur) + vib*math.Sin(2*math.Pi*6*t)
				ph += 2 * math.Pi * f / rate
				x[i] = (math.Sin(ph) + 0.3*math.Sin(2*ph)) * math.Sin(math.Pi*t/dur)
			}
		})
	}
	frog := mono("SE_Frog_Croak", secs(0.6), codecDSP, func(x []float64) {
		for k := 0; k < 3; k++ {
			tone(x, secs(0.18*float64(k)), 0.15, 140, 0.8, 8, 12)
		}
	})
	return []sound{
		sweep("SE_Wolf_Howl", 2.4, 380, 620, 8),
		sweep("SE_Owl_Hoot", 0.9, 330, 360, 2),
		sweep("SE_Bird_Call", 0.5, 2400, 3400, 120),
		frog,
		mono("SE_Bat_Flutter", secs(1.2), codecDSP, func(x []float64) {
			noise(r, x, 0, len(x), 3000, func(t float64) float64 { return 0.5 + 0.5*math.Sin(2*math.Pi*18*t) })
		}),
	}
}

// narrator makes vowel-like formant phrases; it is not speech.
func narrator(r *rand.Rand) []sound {
	var out []sound
	for i := 1; i <= 6; i++ {
		dur := 1.5 + r.Float64()*2.5
		out = append(out, mono(fmt.Sprintf("VO_Narrator_%02d", i), secs(dur), codecPCM16, func(x []float64) {
			var ph float64
			for j := range x {
				t := float64(j) / rate
				f := 120 + 25*math.Sin(2*math.Pi*0.7*t) + 10*math.Sin(2*math.Pi*3*t)
				ph += 2 * math.Pi * f / rate
				syl := math.Max(0, math.Sin(2*math.Pi*3.2*t))
				var v float64
				for h := 1; h <= 20; h++ {
					hf := f * float64(h)
					v += math.Sin(float64(h)*ph) * (math.Exp(-math.Pow((hf-700)/250, 2)) + 0.6*math.Exp(-math.Pow((hf-1200)/300, 2)))
				}
				x[j] = v * syl * math.Min(1, (dur-t)*4)
			}
		}))
	}
	return out
}

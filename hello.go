package main
import "fmt"
import "net/http"
import "net/url"
//import "io"
import "github.com/gorilla/websocket"
//import "sync"
import "time"
import "github.com/veandco/go-sdl2/sdl"
import "context"
import "strconv"
import "os"
import "bufio"
import "strings"


type Pos struct {
	x, y int
}

type Chunk struct {
	tiles [128*128] uint8
	x0 int
	y0 int
	texture *sdl.Texture
}

type World struct {
	chunks map[struct { x, y int }] *Chunk
	enemies []Pos
}

type LocalPlayer struct {
	x int
	y int
	waitingOnAssertion int
	rewardData []uint8
	floodData []uint8
	tilesData []uint8
	rewardApothem int
}

func loadSpritesheet(renp *sdl.Renderer) *sdl.Texture {
	surf, err := sdl.LoadBMP("sprites.bmp")
	if err != nil { panic(err) }
	texp, err := renp.CreateTextureFromSurface(surf)
	if err != nil { panic(err) }
	return texp
}


// type Message

var LoginUrl string // "http://localhost:2626/loginAction"
var UpdateUrl string // "ws://localhost:2626/gameUpdate"
var Username string
var Password string

func readLoginInfo() {
	f, err := os.Open("login-info.txt")
	if err != nil { panic(err) }
	fmt.Println(LoginUrl, UpdateUrl)
	sc := bufio.NewScanner(f)
	sc.Split(bufio.ScanLines)
	sc.Scan()
	LoginUrl = strings.TrimSpace(sc.Text())
	sc.Scan()
	UpdateUrl = strings.TrimSpace(sc.Text())
	sc.Scan()
	Username = strings.TrimSpace(sc.Text())
	sc.Scan()
	Password = strings.TrimSpace(sc.Text())
	fmt.Println("server: ", LoginUrl, UpdateUrl)
	f.Close()
}

var world World
var winp *sdl.Window
var renp *sdl.Renderer
var locpl LocalPlayer
var WalkVectors = [4]Pos { Pos{0,-1}, Pos{1,0}, Pos{0,1}, Pos{-1,0}}
var MouseX int32
var MouseY int32
var MouseXT int
var MouseYT int
var unprocessedCommands = 0


func modulo(a, b int32) int32 {
	return ((a % b) + b) % b
}
func floordiv(a, b int32) int32 {
	return (a - modulo(a, b)) / b
}
func abs(a int) int {
	if a < 0 {
		return -a
	} else {
		return a
	}
}

func canWalkOnTile(tile uint8) bool {
	return ((tile < 0x81 || tile > 0x88) &&
		(tile >  2) && // 0 (null), 2 (chunk not found)
		(tile != 0x95) && // oven
		(tile != 0x96)) // hospital
}
func canRemoveTile(tile uint8) bool {
	return tile >= 0x81 && tile <= 0x88
}


func getcon(ctx context.Context) (*websocket.Conn) {
	resp, err := http.PostForm(
		LoginUrl,
		url.Values{"username": {Username},"password": {Password}})
	if err != nil { panic("server not up?") }
	defer resp.Body.Close()
	consid := resp.Cookies()[0].Value
//	fmt.Println(consid)
//	body, err := io.ReadAll(resp.Body)
	reqh := http.Header{}
	reqh.Add("Cookie", "connect.sid=" + consid)
	conn, _, _ := websocket.DefaultDialer.DialContext(ctx, UpdateUrl, reqh)
	return conn
}

func emptyChunk(cx, cy int) Chunk {
	var tiles [128*128] uint8
	tex, err := renp.CreateTexture(0, sdl.TEXTUREACCESS_TARGET, 8*128, 8*128)
	if err != nil { panic(err) }
	return Chunk{ tiles, cx, cy, tex }
}

func worldGetTile(x, y int) uint8 {
	cx := x &^ 127
	cy := y &^ 127
	var pos struct { x, y int }
	pos.x = cx
	pos.y = cy
	chunk, exists := world.chunks[pos]
	if !exists {
		return 2
	} else {
		i := (y - cy) * 128 + (x - cx)
		return chunk.tiles[i]
	}
}

func worldGetRange(x0, y0, x2, y2 int) []uint8 {
	w := 1 + x2 - x0
	h := 1 + y2 - y0
	res := make([]uint8, w * h)
	i := 0
	for y := y0; y <= y2; y++ {
	for x := x0; x <= x2; x++ {
		res[i] = worldGetTile(x, y)
		i++
	}}
	return res
}

func generateFlood(apoth int) []uint8 {
	diam := 2 * apoth + 1
	var walkable uint8 = 255
	var unwalkable uint8 = 254

	tiles := locpl.tilesData
	flood := make([]uint8, len(tiles))
	for i := range flood {
		if canWalkOnTile(tiles[i]) {
			flood[i] = walkable
		} else {
			flood[i] = unwalkable
		}
	}
	flood[apoth * diam + apoth] = 0

	todo := make([]int, 0)
	todo = append(todo, apoth * diam + apoth)

	for i := uint8(1); i <= uint8(apoth); i++ {
		todonext := make([]int, 0)
		for _, v := range todo {
			if flood[v - diam] == walkable {
				todonext = append(todonext, v - diam)
				flood[v - diam] = i
			}
			if flood[v + diam] == walkable {
				todonext = append(todonext, v + diam)
				flood[v + diam] = i
			}
			if flood[v + 1] == walkable {
				todonext = append(todonext, v + 1)
				flood[v + 1] = i
			}
			if flood[v - 1] == walkable {
				todonext = append(todonext, v - 1)
				flood[v - 1] = i
			}
		}
		if len(todonext) == 0 { break }
		todo = todonext
	}
	return flood
}

func updatePlayerTiles() {
	apoth := locpl.rewardApothem
	locpl.tilesData = worldGetRange(
		locpl.x - apoth, locpl.y - apoth,
		locpl.x + apoth, locpl.y + apoth)
	updatePlayerFlood()
}

func updatePlayerFlood() {
	locpl.floodData = generateFlood(locpl.rewardApothem)
	updatePlayerReward()
}

func updatePlayerReward() {
//	rmx := int(MouseX) - 300
//	rmy := int(MouseY) - 300
	i := 0
	apoth := locpl.rewardApothem
	flood := locpl.floodData
	tiles := locpl.tilesData
	diam := apoth * 2 + 1
	res := make([]uint8, diam * diam)
	for y := -apoth; y <= apoth; y++ {
	for x := -apoth; x <= apoth; x++ {
		if flood[i] > 32 {
			res[i] = 0
			i++
			continue
		}
		val := uint8(0)
		dx := x - MouseXT
		dy := y - MouseYT
		distq := dx * dx + dy * dy
		if distq == 0 {
			val = 45
		} else if distq <= 50 {
			val = 40
		} else {
			val = 20
		}
		if tiles[i] >= 0x91 && tiles[i] <= 0x94 {
			val += 10
		}
		res[i] = val
		i++
	}}
	for _, enemypos := range world.enemies {
		dx := enemypos.x - locpl.x
		dy := enemypos.y - locpl.y
		if abs(dx) < apoth - 2 && abs(dy) < apoth - 2 {
			for y := dy - 2; y <= dy + 2; y++ {
			for x := dx - 2; x <= dx + 2; x++ {
				res[(x + apoth) + (y + apoth) * diam] = 1
			}}
		}
	}
	locpl.rewardData = res
}

func mostRewardingRelpos() (int, int) {
	i := 0
	apoth := locpl.rewardApothem
	minv := uint8(0)
	minx := 0
	miny := 0
	for y := -apoth; y <= apoth; y++ {
	for x := -apoth; x <= apoth; x++ {
		if locpl.rewardData[i] > minv {
			minv = locpl.rewardData[i]
			minx = x
			miny = y
		}
		i++
	}}
	return minx, miny
}

func moveToRelpos(rx, ry int) {
	apoth := locpl.rewardApothem
	diam := apoth * 2 + 1
	o := apoth + apoth * diam
	tiles := locpl.floodData
	p := o + rx + ry * diam
	stepc := tiles[p]
	if stepc >= uint8(apoth) { return }
	walks := make([]uint8, stepc)
	for i := stepc; i > 0; {
		i--
		if tiles[p - diam] == i {
			walks[i] = 2; p -= diam
		} else if tiles[p - 1] == i {
			walks[i] = 1; p -= 1
		} else if tiles[p + 1] == i {
			walks[i] = 3; p += 1
		} else if tiles[p + diam] == i {
			walks[i] = 0; p += diam
		}
	}
	locpl.x += rx
	locpl.y += ry
	for i := uint8(0); i < stepc; i++ {
		qcWalk(walks[i])
	}
	qcAssertPos()
	qcGetTiles("30")
	updatePlayerTiles()
}

func worldSetTile(x, y int, tile uint8) {
	if tile == 0x95 {
		fmt.Println("rest zone at ", x + 2, y)
	} else if tile == 0x96 {
		fmt.Println("rest zone at ", x - 2, y)
	}
	cx := x &^ 127
	cy := y &^ 127
	var pos struct { x, y int }
	pos.x = cx
	pos.y = cy
	chunk, exists := world.chunks[pos]
	if !exists {
		c := emptyChunk(cx, cy)
		chunk = &c
		world.chunks[pos] = &c
	}
	rx := x - cx
	ry := y - cy
	i := ry * 128 + rx
	if chunk.tiles[i] != tile {
		chunk.tiles[i] = tile
		//t2 := worldGetTile(x, y)
		//if (t2 != tile) {
		//	fmt.Println(t2)
		//	panic("aaaaaa")
		//}
		dstr := &sdl.Rect{ int32(rx*8), int32(ry*8), 8, 8 }
		renp.SetRenderTarget(chunk.texture)
		if tile >= 0x80 && tile <= 0x88 {
			r := ([9] uint8 {255,255,255,238,  0,  0,  0,204,170})[tile - 0x80];
			g := ([9] uint8 {255,  0,170,238,204,204,  0,  0,170})[tile - 0x80];
			b := ([9] uint8 {255,  0,  0,  0,  0,204,255,204,170})[tile - 0x80];
			renp.SetDrawColor(r, g, b, 255)
			renp.FillRect(dstr)
		} else if tile >= 0x89 && tile <= 0x90 {
			renp.SetDrawColor(255, 255, 255, 255)
			renp.FillRect(dstr)
			r := ([9] uint8 {255,255,255,170,170,170,255,238})[tile - 0x89];
			g := ([9] uint8 {170,238,255,255,255,170,170,238})[tile - 0x89];
			b := ([9] uint8 {170,170,170,170,255,255,255,238})[tile - 0x89];
			renp.SetDrawColor(r, g, b, 255)
			inset := int32(2)
			dstr.X += inset
			dstr.Y += inset
			dstr.W -= 2 * inset
			dstr.H -= 2 * inset
			renp.FillRect(dstr)
		} else {
			renp.SetDrawColor(85, 85, 85, 255)
			renp.FillRect(dstr)
		}
	}
}

func cqSetLocalPlayerInfo(cmd map[string] interface{}) {
}

var posAssertions = make([]Pos, 0)

func cqAddEntity(cmd map[string] interface{}) {
	einfo := cmd["entityInfo"].(map[string] interface{})
	clas := einfo["className"].(string)
	if clas == "Player" { return }
	if clas != "Enemy" {
		fmt.Println("unknown entity class", clas)
		return
	}
	pos := einfo["pos"].(map[string] interface{})
	x := int(pos["x"].(float64))
	y := int(pos["y"].(float64))
	world.enemies = append(world.enemies, Pos{x, y})
}

func cqSetLocalPlayerPos(cmd map[string] interface{}) {
	pos := cmd["pos"].(map[string] interface{})
	x := int(pos["x"].(float64))
	y := int(pos["y"].(float64))
	servpos := Pos{x, y}
	asserted := posAssertions[0]
	posAssertions = posAssertions[1:]
	//fmt.Println(locpl.waitingOnAssertion)
	if locpl.waitingOnAssertion == 0 {
		locpl.x = x
		locpl.y = y
		locpl.waitingOnAssertion = -1;
		updatePlayerTiles()
	} else if locpl.waitingOnAssertion > 0 {
		locpl.waitingOnAssertion--;
	} else if asserted != servpos {
		fmt.Println("Server says ", servpos)
		qcAssertPos()
		locpl.waitingOnAssertion = len(posAssertions) - 1
	}
}
func qcAssertPos() {
	//xs := strconv.Itoa(int(locpl.x))
	//ys := strconv.Itoa(int(locpl.y))
	pos := Pos{locpl.x, locpl.y}
	posAssertions = append(posAssertions, pos)
	qc("\"commandName\":\"assertPos\",\"pos\":{\"x\":0.5,\"y\":0.5}")
}

func qcGetEntities() {
	qc("\"commandName\":\"getEntities\"")
}

func cqSetTiles(cmd map[string] interface{}) {
	x := int(cmd["pos"].(map[string] interface{})["x"].(float64))
	y := int(cmd["pos"].(map[string] interface{})["y"].(float64))
	sz := int(cmd["size"].(float64))
	ls := cmd["tileList"].([]interface{})
	i := 0
	for yi := y; yi < y + sz; yi++ {
	for xi := x; xi < x + sz; xi++ {
		worldSetTile(xi, yi, uint8(ls[i].(float64)))
		i++
	}
	}
	updatePlayerTiles()
}

var comque = make([]string, 0)

func qcSend(conn *websocket.Conn) {
	if len(comque) == 0 { return }
	s := "[{"
	for i := 0; i < len(comque); i++ {
		s += comque[i]
		if i + 1 == len(comque) { break }
		s += "},{"
	}
	s += "}]"
	comque = make([]string, 0)
	//fmt.Println(s)
	conn.WriteMessage(websocket.TextMessage, []byte (s))
}

func qc(cmd string) {
	comque = append(comque, cmd)
}

func qcWalk(dir uint8) {
	qc("\"commandName\":\"walk\",\"direction\":" + strconv.Itoa(int(dir)))
}

func qcGetTiles(sz string) {
	qc("\"commandName\":\"getTiles\",\"size\":" + sz)
}

func draw() {
	err := renp.SetRenderTarget(nil)
	renp.SetDrawColor(0, 0, 0, 255)
	renp.Clear()

	cxt0 := 300-4
	cyt0 := 300-4
	for _, v := range world.chunks {
		if err != nil { panic(err) }
		srcr := &sdl.Rect{ 0, 0, 128*8, 128*8 }
		dstr := &sdl.Rect{
			int32((v.x0 - locpl.x)*8 + cxt0),
			int32((v.y0 - locpl.y)*8 + cyt0),
			128*8, 128*8 }
		renp.Copy(v.texture, srcr, dstr)
	}

	renp.SetDrawBlendMode(sdl.BLENDMODE_BLEND)

	{
		renp.SetDrawColor(170, 0, 255, 170)
		dstr := &sdl.Rect{
			int32(cxt0),
			int32(cyt0),
			8, 8 }
		renp.FillRect(dstr)
	}

	for _, enemypos := range world.enemies {
		renp.SetDrawColor(255, 0, 170, 170)
		dstr := &sdl.Rect{
			int32((enemypos.x - locpl.x)*8 + cxt0  - 8),
			int32((enemypos.y - locpl.y)*8 + cyt0  - 8),
			24, 24 }
		renp.FillRect(dstr)
	}

	apoth := locpl.rewardApothem
	tiles := locpl.rewardData
	i := 0
	for y := -apoth; y <= apoth; y++ {
	for x := -apoth; x <= apoth; x++ {
		v := tiles[i] * 5
		if v > 200 {
			v = 200
		}
		renp.SetDrawColor(0, 0, 0, v)
		dstr := &sdl.Rect{
			int32(x * 8 + cxt0),
			int32(y * 8 + cyt0), 8, 8}
		renp.FillRect(dstr)
		i++
	}}
	renp.SetDrawBlendMode(sdl.BLENDMODE_NONE)
	renp.Present()
}

func playerWalk(dir uint8) {
	if locpl.waitingOnAssertion < 0 {
		wv := WalkVectors[dir]
		locpl.x += wv.x
		locpl.y += wv.y
		qcWalk(dir)
		qcAssertPos()
	}
}

func gameloop(ch <-chan map[string]interface{}, conn *websocket.Conn) {
	locpl.waitingOnAssertion = 0
	updatePlayerTiles()
	qcAssertPos()
	qcGetTiles("30")
	qc("\"commandName\":\"startPlaying\"")
	qcSend(conn)
	li := 0
	gloop:
	for {
		regenReward := false
		for ; unprocessedCommands > 0; {
			unprocessedCommands--
			v := <-ch
			switch v["commandName"].(string) {
			case "setLocalPlayerPos":
				cqSetLocalPlayerPos(v)
			case "setLocalPlayerInfo":
				cqSetLocalPlayerInfo(v)
			case "setRespawnPos":
			case "setTiles":
				cqSetTiles(v)
			case "removeAllEntities":
				world.enemies = make([]Pos, 0)
				regenReward = true
			case "addEntity":
				cqAddEntity(v)
				regenReward = true
			case "setInventory":
			default:
				fmt.Println(v["commandName"].(string))
			}
		}
		if regenReward { updatePlayerReward() }
		for ev := sdl.PollEvent(); ev != nil; ev = sdl.PollEvent() {
			switch ev.(type) {
			case *sdl.QuitEvent:
				break gloop
			case *sdl.MouseButtonEvent:
				e := ev.(*sdl.MouseButtonEvent)
				if e.Type == sdl.MOUSEBUTTONUP {
					//cxt0 := int32(300-4)
					//cyt0 := int32(300-4)
					//rx := int(floordiv(e.X - cxt0, 8))
					//ry := int(floordiv(e.Y - cyt0, 8))
					//moveToRelpos(rx, ry)
					x, y := mostRewardingRelpos()
					moveToRelpos(x, y)
				}
			case *sdl.MouseMotionEvent:
				e := ev.(*sdl.MouseMotionEvent)
				MouseX = e.X
				MouseY = e.Y
				cxt0 := int32(300-4)
				cyt0 := int32(300-4)
				MouseXT = int(floordiv(e.X - cxt0, 8))
				MouseYT = int(floordiv(e.Y - cyt0, 8))
				updatePlayerReward()
			}
		}
		//playerWalk(0)
		if li % 10 == 0 {
			qcGetEntities()
		}
		qcSend(conn)
		draw()
		t, _ := time.ParseDuration("50ms")
		time.Sleep(t)
		li++
	}
	fmt.Println("gameloop finished")
}

func readloop(ctx context.Context, ch chan<- map[string]interface{}, conn *websocket.Conn) {
	for {
		var v map[string] interface {}
		errt := conn.ReadJSON(&v)
		if ctx.Err() != nil {
			break
		}
		if errt != nil {
			fmt.Println("oh no")
		}
		if v["success"].(bool) {
			cmds := v["commandList"].([]interface {})
			unprocessedCommands += len(cmds)
			for _, cmd := range cmds {
				ch <- cmd.(map[string] interface{})
			}
		} else {
			fmt.Println("success", v["success"])
		}
	}
	fmt.Println("readloop finished")
}

func main() {
	readLoginInfo()
	world.chunks = make(map[struct{x, y int}] *Chunk)
	fmt.Println("Hello word")
	err := sdl.Init(sdl.INIT_EVERYTHING)
	if err != nil { panic(err) }
	win, ren, err := sdl.CreateWindowAndRenderer(600, 600, 0)
	winp = win
	renp = ren
	locpl.x = 0
	locpl.y = 0
	locpl.rewardApothem = 33
	if err != nil { panic(err) }
	defer winp.Destroy()
	defer renp.Destroy()

	ctx, cancel := context.WithCancel(context.Background())
	conn := getcon(ctx)
	cmds := make(chan map[string]interface{})
//	var wg sync.WaitGroup
//	wg.Add(2)
	go readloop(ctx, cmds, conn)
//	go gameloop(cmds, conn, cancel)
	gameloop(cmds, conn)
//	wg.Wait()
	fmt.Println("Hello world")
	ctx.Done()
	cancel()
//	close(cmds)
	sdl.Quit()
}

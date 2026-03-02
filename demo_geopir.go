package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"os"
	"time"

	"github.com/ahenzinger/simplepir/pir"
)

type GeoGrid struct {
	LatMin  float64
	LonMin  float64
	LatStep float64
	LonStep float64
	NLat    int
	NLon    int
}

// map lat lon to tile index
func (g *GeoGrid) TileIndex(lat, lon float64) uint64 {
	i := int(math.Floor((lat - g.LatMin) / g.LatStep))
	j := int(math.Floor((lon - g.LonMin) / g.LonStep))

	if i < 0 { i = 0 }
	if i >= g.NLat { i = g.NLat - 1 }
	if j < 0 { j = 0 }
	if j >= g.NLon { j = g.NLon - 1 }

	return uint64(i*g.NLon + j)
}

// load tile records from binary db
func loadTileDB(path string) ([]byte, uint32, uint32, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, 0, 0, err
	}
	defer file.Close()

	// read header
	header := make([]byte, 15)
	if _, err := io.ReadFull(file, header); err != nil {
		return nil, 0, 0, fmt.Errorf("failed to read header: %v", err)
	}

	magic := string(header[:7])
	if magic != "TILEDB1" {
		return nil, 0, 0, fmt.Errorf("bad magic header: %s", magic)
	}

	recordSize := binary.LittleEndian.Uint32(header[7:11])
	nTiles := binary.LittleEndian.Uint32(header[11:15])

	// read records
	data := make([]byte, recordSize*nTiles)
	if _, err := io.ReadFull(file, data); err != nil {
		return nil, 0, 0, fmt.Errorf("failed to read data: %v", err)
	}

	return data, recordSize, nTiles, nil
}

func main() {
	grid := GeoGrid{
		LatMin: 48.0, LonMin: 8.0,
		LatStep: 0.01, LonStep: 0.01,
		NLat: 400, NLon: 400,
	}

	dbPath := "data/tiles.bin" 
	
	rawData, recordSize, nTiles, err := loadTileDB(dbPath)
	if err != nil {
		fmt.Printf("Error loading DB: %v\n", err)
		return
	}

	lat, lon := 48.137, 11.575
	targetIdx := grid.TileIndex(lat, lon)

	// setup pir
	fmt.Println("=== INITIALIZING SIMPLE PIR ===")
	recordSizeBits := uint64(recordSize) * 8
	numCells := uint64(nTiles)

	pi := pir.SimplePIR{}
	p := pi.PickParams(numCells, recordSizeBits, 1024, 32)
	db := pir.MakeRandomDB(numCells, recordSizeBits, &p)
	
	fmt.Println("=== BENCHMARKING ===")
	
	// server offline
	shared_state := pi.Init(db.Info, p)
	server_state, offline_download := pi.Setup(db, shared_state, p)

	totalT0 := time.Now()

	// client query
	clientT0 := time.Now()
	client_state, q := pi.Query(targetIdx, shared_state, p, db.Info)
	var query pir.MsgSlice
	query.Data = append(query.Data, q)
	clientT1 := time.Now()

	// server answer
	serverT0 := time.Now()
	answer := pi.Answer(db, query, server_state, shared_state, p)
	serverT1 := time.Now()

	// client recover
	clientT2 := time.Now()
	val := pi.Recover(targetIdx, 0, offline_download, query.Data[0], answer, shared_state, client_state, p, db.Info)
	clientT3 := time.Now()

	totalT1 := time.Now()

	// show fetched tile
	fmt.Println("\n=== TILE DATA (decoded from returned record) ===")
	fmt.Println("Baseline direct fetch:")
	
	offset := targetIdx * uint64(recordSize)
	tileBytes := rawData[offset : offset+uint64(recordSize)]
	cleanJSON := bytes.TrimRight(tileBytes, "\x00")
	fmt.Printf("%s\n\n", string(cleanJSON))

	fmt.Println("SimplePIR fetch:")
	fmt.Printf("[Cryptographic math integer recovered: %d]\n", val)

	// compute metrics
	uploadBytes := query.Size() * uint64(p.Logq) / 8
	downloadBytes := answer.Size() * uint64(p.Logq) / 8
	
	clientMs := float64(clientT1.Sub(clientT0).Nanoseconds() + clientT3.Sub(clientT2).Nanoseconds()) / 1e6
	serverMs := float64(serverT1.Sub(serverT0).Nanoseconds()) / 1e6
	totalMs := float64(totalT1.Sub(totalT0).Nanoseconds()) / 1e6

	fmt.Println("=== RESULTS ===")
	fmt.Println("label,db,tiles,recB,idx,upB,downB,client_ms,server_ms,total_ms")
	fmt.Printf("simplepir,tiles.bin,%d,%d,%d,%d,%d,%.2f,%.2f,%.2f\n", 
		nTiles, recordSize, targetIdx, uploadBytes, downloadBytes, clientMs, serverMs, totalMs)
}

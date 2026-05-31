.PHONY: run build tidy clean test

# Jalankan server (development)
run:
	go run ./cmd/server

# Build binary
build:
	go build -o hermes ./cmd/server

# Tidy dependencies
tidy:
	go mod tidy

# Bersihkan binary
clean:
	rm -f hermes

# Test semua package
test:
	go test ./... -v

# Cek apakah bisa compile (tanpa run)
check:
	go build ./...

# Contoh curl: buat entry
curl-create:
	curl -s -X POST http://localhost:8080/api/entries \
		-H "Content-Type: application/json" \
		-d '{"raw_message":"Buat reminder rapat besok jam 10, target: Grup PC IPNU, pesan: Halo {name}! Ada rapat jam 10 pagi. Kirim 1 jam sebelum.", "user_id":"628111111111"}' \
		| python3 -m json.tool

# Contoh curl: simulasi WA
curl-simulate:
	curl -s -X POST http://localhost:8080/api/simulate \
		-H "Content-Type: application/json" \
		-d '{"from":"628111111111","message":"Kirim pengumuman ke Grup PC IPNU: Rapat malam ini jam 20:00, wajib hadir!"}' \
		| python3 -m json.tool

# List semua entry
curl-list:
	curl -s http://localhost:8080/api/entries | python3 -m json.tool

# Health check
curl-health:
	curl -s http://localhost:8080/health | python3 -m json.tool

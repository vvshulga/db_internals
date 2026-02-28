.PHONY: all backend frontend dev-backend dev-frontend test-backend clean setup

all: backend frontend

backend:
	cd backend && go build -o ../ui_admin_server .

frontend:
	cd frontend && npm install && npm run build

dev-backend:
	cd backend && DB_DIR=../testdata PORT=8080 go run .

dev-frontend:
	cd frontend && npm run dev

test-backend:
	cd backend && go test -v -race ./...

clean:
	rm -f ui_admin_server
	rm -rf frontend/dist frontend/node_modules

setup:
	cd frontend && npm install

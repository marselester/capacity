FROM golang:1.16
WORKDIR /opt/demo/
COPY . .
RUN go build -race -o /bin/ ./cmd/...

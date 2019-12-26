FROM golang:1.13
WORKDIR /opt/demo/
COPY . .
RUN go build -race -o /bin/ ./cmd/...

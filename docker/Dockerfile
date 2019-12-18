FROM golang:1.13-alpine AS build
LABEL stage=intermediate
RUN apk add --no-cache git
WORKDIR /opt/demo/
COPY . .
RUN CGO_ENABLED=0 go install \
    --ldflags "-s" -a -installsuffix cgo \
    ./cmd/...

FROM scratch
USER nobody
COPY --from=build /etc/passwd /etc/passwd
COPY --from=build /go/bin/origin /bin/origin
COPY --from=build /go/bin/client /bin/client
COPY --from=build /go/bin/proxy /bin/proxy

FROM alpine

WORKDIR /app

COPY frontail .

EXPOSE 8080

ENTRYPOINT ["/app/frontail"]
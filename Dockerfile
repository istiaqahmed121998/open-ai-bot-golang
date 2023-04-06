FROM golang:1.19.5-buster

ARG PORT
ARG OPENAI_KEY

WORKDIR /build
ENV OPENAI_KEY $OPENAI_KEY

COPY . .

RUN go mod download
RUN go build -o main .

EXPOSE $PORT

ENTRYPOINT ["./main"]
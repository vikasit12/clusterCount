FROM golang:1.20

# Set destination for COPY
RUN mkdir /app
WORKDIR /app

# Download Go modules
COPY go.mod go.sum ./
RUN go mod download

# Copy the source code. 
COPY main.go ./

# Build
RUN CGO_ENABLED=0 GOOS=linux go build -o /cluster-count

# Run
CMD ["/cluster-count"]

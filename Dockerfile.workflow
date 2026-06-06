# we could use multistage but the binary is already built so lets use it
FROM scratch

ARG TARGETOS
ARG TARGETARCH

# Copy the built binary from the builder stage
COPY bop-$TARGETOS-$TARGETARCH bop

# Expose the port your application listens on (optional, but good practice)
EXPOSE 80

# Define the command to run when the container starts
ENTRYPOINT ["/bop"]

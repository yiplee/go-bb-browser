FROM gcr.io/distroless/static:nonroot
COPY bb-daemon /usr/local/bin/bb-daemon
COPY bb-browser /usr/local/bin/bb-browser
USER nonroot:nonroot
EXPOSE 8787
ENV BB_BROWSER_LISTEN=0.0.0.0:8787
ENTRYPOINT ["/usr/local/bin/bb-daemon"]

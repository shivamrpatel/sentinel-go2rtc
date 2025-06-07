console.log("main.js loaded.") // Diagnostic log

window.appState = function appState() {
  // Explicitly make global
  console.log("appState function defined.") // Diagnostic log
  return {
    streams: [],
    selectedStreams: {},
    modes: {
      webrtc: true,
      mse: true,
      hls: true,
      mjpeg: true,
    },
    selectAll: false,
    versionInfo: "Loading...",
    configPathInfo: "Loading...",
    initialLoading: true,
    isDarkMode: document.documentElement.classList.contains("dark"), // Sync with initial state
    init() {
      console.log("appState.init() called.") // Diagnostic log
      // Dark mode is initialized by inline script, this just sets Alpine's reactive property
      this.isDarkMode = document.documentElement.classList.contains("dark")

      this.fetchInfo()
      this.reloadStreams().then(() => {
        this.initialLoading = false
      })
      setInterval(() => this.reloadStreams(), 2000)

      // Listen for system dark mode changes to update Alpine state if no user override
      window.matchMedia("(prefers-color-scheme: dark)").addEventListener("change", (e) => {
        if (localStorage.getItem("darkMode") === null) {
          this.isDarkMode = e.matches
          if (this.isDarkMode) {
            document.documentElement.classList.add("dark")
          } else {
            document.documentElement.classList.remove("dark")
          }
        }
      })
    },
    toggleDarkMode() {
      this.isDarkMode = !this.isDarkMode
      if (this.isDarkMode) {
        document.documentElement.classList.add("dark")
        localStorage.setItem("darkMode", "enabled")
      } else {
        document.documentElement.classList.remove("dark")
        localStorage.setItem("darkMode", "disabled")
      }
    },
    fetchInfo() {
      const url = new URL("api", location.href)
      fetch(url, { cache: "no-cache" })
        .then((r) => (r.ok ? r.json() : Promise.reject(`API error: ${r.status}`)))
        .then((data) => {
          this.versionInfo = data.version || "N/A"
          this.configPathInfo = data.config_path || "N/A"
        })
        .catch((error) => {
          console.error("Error fetching API info:", error)
          this.versionInfo = "Error"
          this.configPathInfo = "Error"
        })
    },
    async reloadStreams() {
      const url = new URL("api/streams", location.href)
      try {
        const response = await fetch(url, { cache: "no-cache" })
        if (!response.ok) {
          console.error("Error fetching streams:", response.status)
          return
        }
        const data = await response.json()

        const newStreamsData = []
        const currentSelectedStates = { ...this.selectedStreams }

        for (const [key, value] of Object.entries(data)) {
          const name = key.replace(/[<">]/g, "")
          const online = value && value.consumers ? value.consumers.length : 0
          const src = encodeURIComponent(name)

          newStreamsData.push({
            id: name,
            name: name,
            online: online,
            src: src,
            infoLink: `api/streams?src=${src}`,
            probeLink: `api/streams?src=${src}&video=all&audio=all&microphone`,
            netLink: `network.html?src=${src}`,
            checked: currentSelectedStates[name] || false,
          })
          if (typeof this.selectedStreams[name] === "undefined") {
            this.selectedStreams[name] = false
          }
        }
        this.streams = newStreamsData
        this.updateSelectAllCheckboxState()
      } catch (error) {
        console.error("Error reloading streams:", error)
      }
    },
    toggleSelectAll(isChecked) {
      this.selectAll = isChecked
      this.streams.forEach((stream) => {
        stream.checked = this.selectAll
        this.selectedStreams[stream.name] = this.selectAll
      })
    },
    updateStreamSelection(streamName) {
      const stream = this.streams.find((s) => s.name === streamName)
      if (stream) {
        this.selectedStreams[streamName] = stream.checked
      }
      this.updateSelectAllCheckboxState()
    },
    updateSelectAllCheckboxState() {
      if (this.streams.length === 0) {
        this.selectAll = false
      } else {
        this.selectAll = this.streams.every((s) => s.checked)
      }
    },
    handleStreamButtonClick() {
      const url = new URL("stream.html", location.href)
      let hasSelection = false
      this.streams.forEach((stream) => {
        if (this.selectedStreams[stream.name]) {
          url.searchParams.append("src", stream.name)
          hasSelection = true
        }
      })

      if (!hasSelection) {
        alert("Please select at least one stream.")
        return
      }

      const selectedModes = Object.entries(this.modes)
        .filter(([_, isSelected]) => isSelected)
        .map(([modeName, _]) => modeName)
        .join(",")

      if (!selectedModes) {
        alert("Please select at least one stream mode.")
        return
      }
      window.location.href = `${url}&mode=${selectedModes}`
    },
    async deleteStream(encodedName) {
      const src = decodeURIComponent(encodedName)
      const message = `Please type the name of the stream "${src}" to confirm its deletion from the configuration. This action is irreversible.`

      if (prompt(message) !== src) {
        alert("Stream name does not match. Deletion cancelled.")
        return
      }

      const deleteUrl = new URL("api/streams", location.href)
      deleteUrl.searchParams.set("src", src)

      try {
        const response = await fetch(deleteUrl, { method: "DELETE" })
        if (!response.ok) {
          throw new Error(`Failed to delete: ${response.status}`)
        }
        this.streams = this.streams.filter((s) => s.name !== src)
        delete this.selectedStreams[src]
        this.updateSelectAllCheckboxState()
      } catch (error) {
        console.error("Failed to delete the stream:", error)
        alert(`Failed to delete stream "${src}". See console for details.`)
      }
    },
  }
}

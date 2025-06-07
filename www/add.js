console.log("add.js loaded.")

function addStreamState() {
  return {
    activeSource: "stream", // Default active tab
    sources: [
      { id: "stream", name: "Temporary Stream" },
      { id: "onvif", name: "ONVIF" },
      { id: "homekit", name: "Apple HomeKit" },
      { id: "hass", name: "Home Assistant" },
      { id: "nest", name: "Google Nest" },
      { id: "ring", name: "Ring" },
      { id: "dvrip", name: "DVRIP" },
      { id: "gopro", name: "GoPro" },
      { id: "roborock", name: "Roborock" },
      { id: "webtorrent", name: "WebTorrent" },
      { id: "devices", name: "FFmpeg Devices (USB)" },
      { id: "hardware", name: "FFmpeg Hardware" },
      { id: "alsa", name: "ALSA (Linux audio)" },
      { id: "v4l2", name: "V4L2 (Linux video)" },
    ],
    forms: {
      stream: { name: "", src: "" },
      onvif: { src: "" },
      homekit: {
        pair: { id: "", url: "", pin: "" },
        unpair: { id: "" },
      },
      nest: {
        client_id: "",
        client_secret: "",
        refresh_token: "",
        project_id: "",
      },
      ring: {
        credentials: { email: "", password: "", code: "" },
        token: { refresh_token: "" },
        show2FA: false,
        tfaPrompt: "",
      },
      roborock: {
        username: "",
        password: "",
      },
    },
    tables: {
      onvif: 'Click "Discover" to search for devices on your network.',
      homekit: "Use the forms above to pair/unpair devices, then devices will appear here.",
      nest: "Enter your Google Nest credentials above to see available devices.",
      ring: "Login with your Ring credentials above to see available devices.",
      roborock: "Login with your Roborock credentials above to see available devices.",
      alsa: "Click the 'ALSA' source type to load available devices.",
      dvrip: "Click the 'DVRIP' source type to load available devices.",
      devices: "Click the 'FFmpeg Devices' source type to load available devices.",
      hardware: "Click the 'FFmpeg Hardware' source type to load available devices.",
      gopro: "Click the 'GoPro' source type to load available devices.",
      hass: "Click the 'Home Assistant' source type to load available devices.",
      v4l2: "Click the 'V4L2' source type to load available devices.",
      webtorrent: "Click the 'WebTorrent' source type to load available shares.",
    },

    setActiveSource(sourceId) {
      this.activeSource = sourceId
      const source = this.sources.find((s) => s.id === sourceId)
      if (!source) return

      const simpleSources = ["alsa", "dvrip", "devices", "hardware", "gopro", "hass", "v4l2", "webtorrent"]
      if (simpleSources.includes(sourceId)) {
        this.getSources(sourceId, `api/${sourceId}`)
      } else if (
        sourceId === "onvif" &&
        typeof this.tables.onvif === "string" &&
        this.tables.onvif.includes("Discover")
      ) {
        this.getSources("onvif", "api/onvif") // Initial load for ONVIF
      } else if (sourceId === "homekit") {
        this.reloadHomeKit() // Load HomeKit devices when tab is selected
      }
      // For sources with forms (Nest, Ring, Roborock), data is loaded after form submission.
    },

    async getSources(tableKey, url) {
      this.tables[tableKey] = `<div class="flex justify-center items-center h-32">
                                <div class="animate-spin inline-block size-6 border-[3px] border-current border-t-transparent text-blue-600 rounded-full" role="status" aria-label="loading">
                                    <span class="sr-only">Loading...</span>
                                </div>
                                <span class="ml-2 text-gray-500 dark:text-gray-400">Loading...</span>
                               </div>`
      try {
        const response = typeof url === "string" ? await fetch(url, { cache: "no-cache" }) : url
        if (!response.ok) {
          this.tables[tableKey] =
            `<p class="text-red-600 dark:text-red-500 p-4 bg-red-100 dark:bg-red-500/10 rounded-lg">Error: ${await response.text()}</p>`
          return
        }
        const data = await response.json()
        if (data && data.sources && data.sources.length > 0) {
          this.tables[tableKey] = data
        } else {
          this.tables[tableKey] = "No sources found."
        }
      } catch (error) {
        console.error(`Error fetching sources for ${tableKey}:`, error)
        this.tables[tableKey] =
          `<p class="text-red-600 dark:text-red-500 p-4 bg-red-100 dark:bg-red-500/10 rounded-lg">Failed to fetch data. See console for details.</p>`
      }
    },

    renderTable(data, sourceId = "") {
      if (typeof data === "string") {
        return `<div class="p-4 rounded-lg bg-gray-50 dark:bg-slate-800 text-gray-600 dark:text-gray-400">${data}</div>`
      }
      if (!data || !data.sources || data.sources.length === 0) {
        return `<div class="p-4 rounded-lg bg-gray-50 dark:bg-slate-800 text-gray-600 dark:text-gray-400">No sources found.</div>`
      }

      const defaultCols = ["id", "name", "info", "url", "location"]
      const headers = data.sources[0] ? Object.keys(data.sources[0]).filter((k) => defaultCols.includes(k)) : []

      // Special handling for HomeKit table to add action buttons
      const isHomeKitTable = sourceId === "homekit"
      if (isHomeKitTable && headers.length > 0 && !headers.includes("actions")) {
        // headers.push('actions'); // We'll add actions inline for HomeKit
      }

      let thead = "<tr>"
      headers.forEach(
        (h) =>
          (thead += `<th class="px-6 py-3 text-left text-xs font-medium text-gray-500 dark:text-gray-400 uppercase tracking-wider">${h}</th>`),
      )
      if (isHomeKitTable)
        thead += `<th class="px-6 py-3 text-left text-xs font-medium text-gray-500 dark:text-gray-400 uppercase tracking-wider">Actions</th>`
      thead += "</tr>"

      let tbody = ""
      data.sources.forEach((row) => {
        tbody += '<tr class="hover:bg-gray-50 dark:hover:bg-slate-800/50">'
        headers.forEach((h) => {
          tbody += `<td class="px-6 py-4 whitespace-normal break-words text-sm text-gray-800 dark:text-gray-200">${row[h] || ""}</td>`
        })
        if (isHomeKitTable) {
          let commands = ""
          if (row.info && row.info.indexOf("status=1") > -1) {
            // Assuming 'info' contains status
            commands += `<button @click="$dispatch('populate-homekit-pair', { id: '${row.id}', url: '${row.info}' })" class="py-1 px-2 text-xs font-medium rounded-lg border border-transparent bg-blue-500 text-white hover:bg-blue-600">Pair</button>`
          } else if (row.url) {
            // Assuming 'url' indicates a paired device that can be unpaired
            commands += `<button @click="$dispatch('populate-homekit-unpair', { id: '${row.url}' })" class="py-1 px-2 text-xs font-medium rounded-lg border border-transparent bg-red-500 text-white hover:bg-red-600 ml-1">Unpair</button>`
          }
          tbody += `<td class="px-6 py-4 whitespace-nowrap text-sm">${commands}</td>`
        }
        tbody += "</tr>"
      })

      return `<div class="overflow-x-auto -mx-1 sm:-mx-2 lg:-mx-4"><div class="inline-block min-w-full py-2 align-middle sm:px-6 lg:px-8">
                <div class="overflow-hidden border border-gray-200 dark:border-gray-700 sm:rounded-lg">
                    <table class="min-w-full divide-y divide-gray-200 dark:divide-gray-700">
                        <thead class="bg-gray-50 dark:bg-slate-800">${thead}</thead>
                        <tbody class="divide-y divide-gray-200 dark:divide-gray-700 bg-white dark:bg-slate-900">${tbody}</tbody>
                    </table>
                </div></div></div>`
    },

    async addTemporaryStream() {
      const { name, src } = this.forms.stream
      if (!name || !src) {
        alert("Please provide both a name and a URL.") // Consider Preline Toasts/Modals later
        return
      }
      const url = new URL("api/streams", location.href)
      url.searchParams.set("name", name)
      url.searchParams.set("src", src)

      try {
        const r = await fetch(url, { method: "PUT" })
        alert(r.ok ? "Stream added successfully!" : `ERROR: ${await r.text()}`)
        if (r.ok) {
          this.forms.stream = { name: "", src: "" }
        }
      } catch (error) {
        alert("An error occurred while adding the stream.")
        console.error(error)
      }
    },

    async discoverOnvif() {
      const url = new URL("api/onvif", location.href)
      if (this.forms.onvif.src) {
        url.searchParams.set("src", this.forms.onvif.src)
      }
      await this.getSources("onvif", url.toString())
    },

    async submitHomekitPair() {
      const { id, url, pin } = this.forms.homekit.pair
      if (!id || !url || !pin) {
        alert("Please fill in all fields.")
        return
      }

      const body = new FormData()
      body.set("id", id)
      body.set("url", url + "&pin=" + pin)

      try {
        const r = await fetch("api/homekit", { method: "POST", body: body })
        alert(r.ok ? "Device paired successfully!" : `ERROR: ${await r.text()}`)
        if (r.ok) {
          this.forms.homekit.pair = { id: "", url: "", pin: "" }
          await this.reloadHomeKit()
        }
      } catch (error) {
        alert("An error occurred while pairing the device.")
        console.error(error)
      }
    },

    async submitHomekitUnpair() {
      const { id } = this.forms.homekit.unpair
      if (!id) {
        alert("Please provide a stream ID.")
        return
      }

      const body = new FormData()
      body.set("id", id)

      try {
        const r = await fetch("api/homekit", { method: "DELETE", body: body })
        alert(r.ok ? "Device unpaired successfully!" : `ERROR: ${await r.text()}`)
        if (r.ok) {
          this.forms.homekit.unpair = { id: "" }
          await this.reloadHomeKit()
        }
      } catch (error) {
        alert("An error occurred while unpairing the device.")
        console.error(error)
      }
    },

    // Listener for populating HomeKit forms from table actions
    initHomekitFormPopulators() {
      this.$watch("activeSource", (value) => {
        // Ensure this runs in Alpine context
        if (value === "homekit") {
          this.$nextTick(() => {
            // Wait for table to render
            this._homekitPairListener = (event) => {
              this.forms.homekit.pair.id = event.detail.id
              this.forms.homekit.pair.url = event.detail.url
            }
            this._homekitUnpairListener = (event) => {
              this.forms.homekit.unpair.id = event.detail.id
            }
            document.addEventListener("populate-homekit-pair", this._homekitPairListener)
            document.addEventListener("populate-homekit-unpair", this._homekitUnpairListener)
          })
        } else {
          if (this._homekitPairListener)
            document.removeEventListener("populate-homekit-pair", this._homekitPairListener)
          if (this._homekitUnpairListener)
            document.removeEventListener("populate-homekit-unpair", this._homekitUnpairListener)
        }
      })
    },

    async reloadHomeKit() {
      await this.getSources("homekit", "api/homekit")
    },

    async submitNestLogin() {
      const { client_id, client_secret, refresh_token, project_id } = this.forms.nest
      if (!client_id || !client_secret || !refresh_token || !project_id) {
        alert("Please fill in all fields.")
        return
      }

      const query = new URLSearchParams({
        client_id,
        client_secret,
        refresh_token,
        project_id,
      })
      const url = new URL("api/nest?" + query.toString(), location.href)

      try {
        const r = await fetch(url, { cache: "no-cache" })
        await this.getSources("nest", r)
      } catch (error) {
        this.tables.nest = `<p class="text-red-600 dark:text-red-500 p-4 bg-red-100 dark:bg-red-500/10 rounded-lg">Failed to login. See console for details.</p>`
        console.error(error)
      }
    },

    async submitRingCredentials() {
      const { email, password, code } = this.forms.ring.credentials
      if (!email || !password) {
        alert("Please provide email and password.")
        return
      }

      const query = new URLSearchParams({ email, password })
      if (this.forms.ring.show2FA && code) query.set("code", code) // Only add code if 2FA is shown and code is entered

      const url = new URL("api/ring?" + query.toString(), location.href)

      try {
        const r = await fetch(url, { cache: "no-cache" })
        const data = await r.json()

        if (data.needs_2fa) {
          this.forms.ring.show2FA = true
          this.forms.ring.tfaPrompt = data.prompt || "Enter 2FA code"
          return
        }

        if (!r.ok) {
          this.tables.ring = `<p class="text-red-600 dark:text-red-500 p-4 bg-red-100 dark:bg-red-500/10 rounded-lg">${data.error || "Unknown error"}</p>`
          return
        }

        this.tables.ring = data
        this.forms.ring.show2FA = false // Reset 2FA on success
        this.forms.ring.tfaPrompt = ""
        this.forms.ring.credentials.code = "" // Clear 2FA code
      } catch (error) {
        this.tables.ring = `<p class="text-red-600 dark:text-red-500 p-4 bg-red-100 dark:bg-red-500/10 rounded-lg">Failed to login. See console for details.</p>`
        console.error(error)
      }
    },

    async submitRingToken() {
      const { refresh_token } = this.forms.ring.token
      if (!refresh_token) {
        alert("Please provide a refresh token.")
        return
      }

      const query = new URLSearchParams({ refresh_token })
      const url = new URL("api/ring?" + query.toString(), location.href)

      try {
        const r = await fetch(url, { cache: "no-cache" })
        const data = await r.json()

        if (!r.ok) {
          this.tables.ring = `<p class="text-red-600 dark:text-red-500 p-4 bg-red-100 dark:bg-red-500/10 rounded-lg">${data.error || "Unknown error"}</p>`
          return
        }
        this.tables.ring = data
      } catch (error) {
        this.tables.ring = `<p class="text-red-600 dark:text-red-500 p-4 bg-red-100 dark:bg-red-500/10 rounded-lg">Failed to login. See console for details.</p>`
        console.error(error)
      }
    },

    async submitRoborockLogin() {
      const { username, password } = this.forms.roborock
      if (!username || !password) {
        alert("Please provide username and password.")
        return
      }

      const body = new FormData()
      body.set("username", username)
      body.set("password", password)

      try {
        const r = await fetch("api/roborock", { method: "POST", body: body })
        await this.getSources("roborock", r)
      } catch (error) {
        this.tables.roborock = `<p class="text-red-600 dark:text-red-500 p-4 bg-red-100 dark:bg-red-500/10 rounded-lg">Failed to login. See console for details.</p>`
        console.error(error)
      }
    },

    // Call this in init if you want HomeKit form populators
    init() {
      this.initHomekitFormPopulators()
      // ... other init logic
    },
  }
}

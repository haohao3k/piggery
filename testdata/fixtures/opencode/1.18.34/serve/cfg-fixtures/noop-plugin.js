// A plugin that does nothing: stands in for "a plugin entry" in the config merge test (no npm install needed).
export default { id: "noop", server: async () => ({}) }

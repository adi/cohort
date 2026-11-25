import type { Register } from 'claude-code'

// The mod lives at <manager folder>/mods/cohort-team; the tools at
// <manager folder>/bin/<name>. $.plugin.root is the mod's directory.
const tool = (root: string, name: string) =>
  `${root.replace(/\/\.claude-plugin\/?$/, '').replace(/\/$/, '')}/../../bin/${name}`

// Each command runs the bin/ tool of the same name with its arguments.
const commands = [
  {
    name: 'team',
    description: 'Show, hide or list cohort teams (runs bin/team, no model turn)',
    argumentHint: 'list | show <team> | hide <team>',
  },
  {
    name: 'agent',
    description: 'Show, hide or list individual cohort agents (runs bin/agent, no model turn)',
    argumentHint: 'list | show <agent>... | hide <agent>...',
  },
]

export const register: Register = on => {
  on('session.start', async ($, e, next) => {
    for (const c of commands) await $.command.register(c)
    return next(e)
  })

  for (const c of commands) {
    on('command.run', { command: c.name }, async ($, e) => {
      const bin = tool($.plugin.root, c.name)
      const args = e.args.trim().split(/\s+/).filter(Boolean)
      const argv = args.length ? args : ['list']
      try {
        const { exitCode, stdout, stderr } = await $.process.run([bin, ...argv], { timeoutMs: 30_000 })
        const out = [stdout.trim(), stderr.trim()].filter(Boolean).join('\n')
        return { text: exitCode === 0 ? out || 'done' : `${c.name} ${argv.join(' ')} failed (exit ${exitCode}):\n${out}` }
      } catch (err) {
        return { text: `could not run ${bin}: ${String(err)}` }
      }
    })
  }
}

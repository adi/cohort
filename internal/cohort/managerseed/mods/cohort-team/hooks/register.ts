import type { Register } from 'claude-code'

// The mod lives at <manager folder>/mods/cohort-team; the tool at
// <manager folder>/bin/team. $.plugin.root is the mod's directory.
const teamTool = (root: string) =>
  `${root.replace(/\/\.claude-plugin\/?$/, '').replace(/\/$/, '')}/../../bin/team`

export const register: Register = on => {
  on('session.start', async ($, e, next) => {
    await $.command.register({
      name: 'team',
      description: 'Show, hide or list cohort teams (runs bin/team, no model turn)',
      argumentHint: 'list | show <team> | hide <team>',
    })
    return next(e)
  })

  on('command.run', { command: 'team' }, async ($, e) => {
    const team = teamTool($.plugin.root)
    const args = e.args.trim().split(/\s+/).filter(Boolean)
    const argv = args.length ? args : ['list']
    try {
      const { exitCode, stdout, stderr } = await $.process.run([team, ...argv], { timeoutMs: 30_000 })
      const out = [stdout.trim(), stderr.trim()].filter(Boolean).join('\n')
      return { text: exitCode === 0 ? out || 'done' : `team ${argv.join(' ')} failed (exit ${exitCode}):\n${out}` }
    } catch (err) {
      return { text: `could not run ${team}: ${String(err)}` }
    }
  })
}

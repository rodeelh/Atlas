import { render } from 'preact'
import { App } from './App'
import './styles.css'
import { loadTheme, applyTheme, loadAutonomyMode, themeConfigForAutonomyMode } from './theme'

// Apply the last known theme + autonomy override before first paint.
applyTheme(themeConfigForAutonomyMode(loadTheme(), loadAutonomyMode()))

render(<App />, document.getElementById('app')!)

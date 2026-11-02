import { Component, type ErrorInfo, type ReactNode } from 'react'

interface State {
  error: Error | null
}

export default class ErrorBoundary extends Component<{ children: ReactNode }, State> {
  state: State = { error: null }

  static getDerivedStateFromError(error: Error): State {
    return { error }
  }

  componentDidCatch(error: Error, info: ErrorInfo) {
    console.error('UI error boundary', error, info)
  }

  render() {
    if (this.state.error) {
      return (
        <div className="page" style={{ padding: 32 }}>
          <div className="form-error" style={{ display: 'block' }}>
            <b>La interfaz ha encontrado un error.</b>
            <div style={{ marginTop: 8 }}>{this.state.error.message}</div>
            <button className="btn btn-secondary btn-sm" style={{ marginTop: 12 }} onClick={() => location.reload()}>
              Recargar
            </button>
          </div>
        </div>
      )
    }
    return this.props.children
  }
}

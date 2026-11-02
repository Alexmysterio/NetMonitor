import { useEffect, useState } from 'react'
import { AlertTriangle } from 'lucide-react'
import { Button, Modal } from './ui'

type ConfirmRequest = {
  message: string
  title?: string
  confirmLabel?: string
  danger?: boolean
  resolve: (value: boolean) => void
}

let handler: ((request: ConfirmRequest) => void) | null = null

export function confirmAction(message: string, options: { title?: string; confirmLabel?: string; danger?: boolean } = {}): Promise<boolean> {
  return new Promise(resolve => {
    if (!handler) {
      resolve(window.confirm(message))
      return
    }
    handler({ message, ...options, resolve })
  })
}

export function ConfirmHost() {
  const [request, setRequest] = useState<ConfirmRequest | null>(null)

  useEffect(() => {
    handler = setRequest
    return () => { handler = null }
  }, [])

  const finish = (value: boolean) => {
    request?.resolve(value)
    setRequest(null)
  }

  return (
    <Modal
      open={!!request}
      onClose={() => finish(false)}
      title={request?.title || 'Confirmar acción'}
      subtitle="Esta acción requiere confirmación."
      width={470}
      footer={<>
        <Button variant="ghost" onClick={() => finish(false)}>Cancelar</Button>
        <Button variant={request?.danger === false ? 'primary' : 'danger'} onClick={() => finish(true)}>
          {request?.confirmLabel || 'Confirmar'}
        </Button>
      </>}
    >
      <div className="confirm-body">
        <div className="confirm-icon"><AlertTriangle size={20} /></div>
        <p>{request?.message}</p>
      </div>
    </Modal>
  )
}

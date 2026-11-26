  const OrderOverview = () => {
    return (
      <Panel className={`order-overview ${isOrderPending ? 'order-overview--pending' : ''}`}>
        {isOrderPending && <div className='order-overview--pending-indicator'>{labels}</div>}
        <div className='order-overview__top_ribbon' ref={topRibbon} style={{ transform: ribbonOffset }}><img src={RibbonImg} alt='top ribbon' /></div>
        <div className='order-overview__side_badge' ref={sideBadge} style={{ transform: badgeOffsetY }}><img src={BadgeImg} alt='side badge' /></div>
        <div className='order-overview__corner' ref={corner} style={{ transform: cornerOffset }}><img src={CornerImg} alt='corner' /></div>
        <div className="order-overview__order__title">{}</div>
        <div className="order-overview__order__total-label">{labels['orderOverview.total']}</div>
        <div className="order-overview__order__total-amount">{formatPrice(orderTotal, currency) || '-'}</div>
        <div className="order-overview__order__total-breakdown">
          <PriceBreakdown lineItems={{}} />
        </div>
        {
          hasEarlierOrder
            ? <div className='order-overview__order__notice'>
              <div className='order-overview__order__notice__row1'>
                <img className='order-overview__order__notice__row1__icon' src={WarningIcon} />
                <div className='order-overview__order__notice__row1__text'>{labels}</div>
              </div>
              <div className='order-overview__order__notice__row2'>
                <Button size='small' variant="text" onClick={() => {}}>
                  {labels}
                </Button>
              </div>
            </div>
            : <Button className="order-overview__order__checkout" size='large' variant="text" data-testid="order-overview-checkout" onClick={() => {}}>
              {labels && labels['checkout']}
            </Button>
        }

      </Panel>
    );
  };

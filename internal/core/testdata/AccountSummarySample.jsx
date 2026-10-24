  const SummaryPlan = () => {
    return (
      <Card className={`plan-summary ${isPlanTransferInProgressNew ? 'plan-summary--new-plan' : ''}`}>
        {isPlanTransferInProgressNew && <div className='plan-summary--new-plan-indicator'>{localized['accountSummary.newPlanLabel']}</div>}
        <div className='plan-summary__semi_annulus' ref={semiAnnulus} style={{ transform: semiAnnulusTranslate }}><img src={SemiAnnulus} alt='semi annulus' /></div>
        <div className='plan-summary__small_annulus' ref={smallAnnulus} style={{ transform: smallAnnulusTranslateY }}><img src={SmallAnnulus} alt='small annulus' /></div>
        <div className='plan-summary__square' ref={square} style={{ transform: squareTranslate }}><img src={Square} alt='square' /></div>
        <div className="plan-summary__plan__name">{}</div>
        <div className="plan-summary__plan__savings-label">{localized['accountSummary.totalSavings']}</div>
        <div className="plan-summary__plan__savings-amount">{(RespSavings) && formatCurrency(RespSavings, locale) || '-'}</div>
        <div className="plan-summary__plan__savings-return">
          <SavingsReturn planAmounts={{}} />
        </div>
        {
          isPlanTransferInProgressOld
            ? <div className='plan-summary__plan__old-plan-container'>
              <div className='plan-summary__plan__old-plan-container__row1'>
                <img className='plan-summary__plan__old-plan-container__row1__icon' src={ErrorIcon} />
                <div className='plan-summary__plan__old-plan-container__row1__text'>{localized['accountSummary.totalSavings.oldPlanText']}</div>
              </div>
              <div className='plan-summary__plan__old-plan-container__row2'>
                <Button size='small' variant="text" onClick={() => {})}>
                  {localized['accountSummary.totalSavings.viewNewPlanBtn']}
                </Button>
              </div>
            </div>
            : <Button className="plan-summary__plan__contribute" size='large' variant="text" data-testid="account-summary-contribute" onClick={() => {}}>
              {localized && localized['contribute']}
            </Button>
        }

      </Card>
    );
  };
